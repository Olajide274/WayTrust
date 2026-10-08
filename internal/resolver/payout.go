package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	ErrTransferFailed  = errors.New("payment gateway failed to initiate transfer")
	ErrRecipientFailed = errors.New("failed to generate transfer recipient")
)

type PaystackRecipientRequest struct {
	Type          string `json:"type"`
	Name          string `json:"name"`
	AccountNumber string `json:"account_number"`
	BankCode      string `json:"bank_code"`
	Currency      string `json:"currency"`
}

type PaystackRecipientResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		RecipientCode string `json:"recipient_code"`
		Active        bool   `json:"active"`
	} `json:"data"`
}

type PaystackTransferRequest struct {
	Source    string `json:"source"`
	Amount    int64  `json:"amount"`
	Recipient string `json:"recipient"`
	Reason    string `json:"reason"`
	Reference string `json:"reference"`
}

type PaystackTransferResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Reference       string `json:"reference"`
		TransferCode    string `json:"transfer_code"`
		Status          string `json:"status"`
		GatewayResponse string `json:"gateway_response"`
	} `json:"data"`
}

type PayoutTransfer struct {
	Reference  string
	Status     string
	HTTPStatus int
	Message    string
}

type PayoutError struct {
	HTTPStatus     int
	Message        string
	TransferStatus string
	Err            error
}

func (e *PayoutError) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("Paystack returned HTTP %d: %s", e.HTTPStatus, e.Message)
	}
	if e.Message != "" {
		return e.Message
	}
	return "Paystack payout request failed"
}

func (e *PayoutError) Unwrap() error {
	return e.Err
}

type PayoutDisburser interface {
	InitiateDriverPayout(ctx context.Context, accountName, accountNo, bankCode string, amountKobo int64, reference string) (PayoutTransfer, error)
	VerifyTransfer(ctx context.Context, reference string) (PayoutTransfer, error)
}

type PaystackPayoutDisburser struct {
	httpClient *http.Client
	baseURL    string
}

func NewPaystackPayoutDisburser(timeout time.Duration) *PaystackPayoutDisburser {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &PaystackPayoutDisburser{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    "https://api.paystack.co",
	}
}

func (d *PaystackPayoutDisburser) InitiateDriverPayout(
	ctx context.Context,
	accountName string,
	accountNo string,
	bankCode string,
	amountKobo int64,
	reference string,
) (PayoutTransfer, error) {
	apiKey := os.Getenv("PAYSTACK_SECRET_KEY")
	if apiKey == "" {
		return PayoutTransfer{}, ErrMissingSecretKey
	}

	recipientPayload := PaystackRecipientRequest{
		Type:          "nuban",
		Name:          accountName,
		AccountNumber: accountNo,
		BankCode:      bankCode,
		Currency:      "NGN",
	}

	reqBody, err := json.Marshal(recipientPayload)
	if err != nil {
		return PayoutTransfer{}, fmt.Errorf("failed to marshal recipient request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/transferrecipient", bytes.NewBuffer(reqBody))
	if err != nil {
		return PayoutTransfer{}, fmt.Errorf("failed to create recipient request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return PayoutTransfer{}, &PayoutError{Message: err.Error(), Err: ErrRecipientFailed}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var errBody struct {
			Status  bool   `json:"status"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		msg := errBody.Message
		if msg == "" {
			msg = "no message from gateway"
		}
		return PayoutTransfer{}, &PayoutError{HTTPStatus: resp.StatusCode, Message: msg, Err: ErrRecipientFailed}
	}

	var recipientResp PaystackRecipientResponse
	if err := json.NewDecoder(resp.Body).Decode(&recipientResp); err != nil || !recipientResp.Status {
		msg := "invalid recipient response"
		if err != nil {
			msg = err.Error()
		} else if recipientResp.Message != "" {
			msg = recipientResp.Message
		}
		return PayoutTransfer{}, &PayoutError{HTTPStatus: resp.StatusCode, Message: msg, Err: ErrRecipientFailed}
	}

	transferPayload := PaystackTransferRequest{
		Source:    "balance",
		Amount:    amountKobo,
		Recipient: recipientResp.Data.RecipientCode,
		Reason:    fmt.Sprintf("NURTW Waybill Settlement Ref: %s", reference),
		Reference: reference,
	}

	transferBody, err := json.Marshal(transferPayload)
	if err != nil {
		return PayoutTransfer{}, fmt.Errorf("failed to marshal transfer request: %w", err)
	}

	tReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/transfer", bytes.NewBuffer(transferBody))
	if err != nil {
		return PayoutTransfer{}, fmt.Errorf("failed to create transfer request: %w", err)
	}
	tReq.Header.Set("Authorization", "Bearer "+apiKey)
	tReq.Header.Set("Content-Type", "application/json")

	tResp, err := d.httpClient.Do(tReq)
	if err != nil {
		return PayoutTransfer{}, &PayoutError{Message: err.Error(), Err: ErrTransferFailed}
	}
	defer tResp.Body.Close()

	if tResp.StatusCode != http.StatusOK && tResp.StatusCode != http.StatusCreated {
		var errBody struct {
			Status  bool   `json:"status"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(tResp.Body).Decode(&errBody)
		msg := errBody.Message
		if msg == "" {
			msg = "no message from gateway"
		}
		if isDuplicateTransferReference(msg) {
			return d.VerifyTransfer(ctx, reference)
		}
		return PayoutTransfer{}, &PayoutError{HTTPStatus: tResp.StatusCode, Message: msg, Err: ErrTransferFailed}
	}

	var transferResult PaystackTransferResponse
	if err := json.NewDecoder(tResp.Body).Decode(&transferResult); err != nil {
		return PayoutTransfer{}, &PayoutError{
			HTTPStatus: tResp.StatusCode,
			Message:    err.Error(),
			Err:        ErrTransferFailed,
		}
	}
	if !transferResult.Status {
		msg := transferResult.Message
		if msg == "" {
			msg = "transfer initiation failed"
		}
		if isDuplicateTransferReference(msg) {
			return d.VerifyTransfer(ctx, reference)
		}
		return PayoutTransfer{}, &PayoutError{HTTPStatus: tResp.StatusCode, Message: msg, Err: ErrTransferFailed}
	}

	message := transferResult.Data.GatewayResponse
	if message == "" {
		message = transferResult.Message
	}
	return PayoutTransfer{
		Reference:  transferResult.Data.Reference,
		Status:     strings.ToLower(transferResult.Data.Status),
		HTTPStatus: tResp.StatusCode,
		Message:    message,
	}, nil
}

func (d *PaystackPayoutDisburser) VerifyTransfer(ctx context.Context, reference string) (PayoutTransfer, error) {
	apiKey := os.Getenv("PAYSTACK_SECRET_KEY")
	if apiKey == "" {
		return PayoutTransfer{}, ErrMissingSecretKey
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		d.baseURL+"/transfer/verify/"+url.PathEscape(reference),
		nil,
	)
	if err != nil {
		return PayoutTransfer{}, fmt.Errorf("failed to create transfer verification request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return PayoutTransfer{}, &PayoutError{Message: err.Error(), Err: ErrTransferFailed}
	}
	defer resp.Body.Close()

	var result PaystackTransferResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return PayoutTransfer{}, &PayoutError{
			HTTPStatus: resp.StatusCode,
			Message:    err.Error(),
			Err:        ErrTransferFailed,
		}
	}
	if resp.StatusCode != http.StatusOK || !result.Status {
		message := result.Message
		if message == "" {
			message = "transfer reference was not found"
		}
		return PayoutTransfer{}, &PayoutError{
			HTTPStatus: resp.StatusCode,
			Message:    message,
			Err:        ErrTransferFailed,
		}
	}

	message := result.Data.GatewayResponse
	if message == "" {
		message = result.Message
	}
	return PayoutTransfer{
		Reference:  result.Data.Reference,
		Status:     strings.ToLower(result.Data.Status),
		HTTPStatus: resp.StatusCode,
		Message:    message,
	}, nil
}

func isDuplicateTransferReference(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "duplicate") ||
		(strings.Contains(message, "already exists") && strings.Contains(message, "reference"))
}
