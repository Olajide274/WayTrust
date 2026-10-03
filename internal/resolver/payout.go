package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
		Reference    string `json:"reference"`
		TransferCode string `json:"transfer_code"`
		Status       string `json:"status"`
	} `json:"data"`
}

type PayoutDisburser interface {
	InitiateDriverPayout(ctx context.Context, accountName, accountNo, bankCode string, amountKobo int64, reference string) (string, error)
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
) (string, error) {
	apiKey := os.Getenv("PAYSTACK_SECRET_KEY")
	if apiKey == "" {
		return "", ErrMissingSecretKey
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
		return "", fmt.Errorf("failed to marshal recipient request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/transferrecipient", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create recipient request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRecipientFailed, err)
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
		return "", fmt.Errorf("%w: gateway returned status %d: %s", ErrRecipientFailed, resp.StatusCode, msg)
	}

	var recipientResp PaystackRecipientResponse
	if err := json.NewDecoder(resp.Body).Decode(&recipientResp); err != nil || !recipientResp.Status {
		return "", fmt.Errorf("%w: invalid gateway response: %v", ErrRecipientFailed, err)
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
		return "", fmt.Errorf("failed to marshal transfer request: %w", err)
	}

	tReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/transfer", bytes.NewBuffer(transferBody))
	if err != nil {
		return "", fmt.Errorf("failed to create transfer request: %w", err)
	}
	tReq.Header.Set("Authorization", "Bearer "+apiKey)
	tReq.Header.Set("Content-Type", "application/json")

	tResp, err := d.httpClient.Do(tReq)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTransferFailed, err)
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
		return "", fmt.Errorf("%w: gateway returned HTTP %d: %s", ErrTransferFailed, tResp.StatusCode, msg)
	}

	var transferResult PaystackTransferResponse
	if err := json.NewDecoder(tResp.Body).Decode(&transferResult); err != nil || !transferResult.Status {
		return "", fmt.Errorf("%w: transfer initiation failed", ErrTransferFailed)
	}

	return transferResult.Data.Reference, nil
}