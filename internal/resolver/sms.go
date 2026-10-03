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
	ErrSMSDispatchFailed = errors.New("failed to dispatch transactional SMS via gateway")
	ErrMissingSMSConfig  = errors.New("SMS gateway API key is not configured in environment")
)

type TermiiSMSRequest struct {
	To      string `json:"to"`
	From    string `json:"from"`
	Sms     string `json:"sms"`
	Type    string `json:"type"`
	Channel string `json:"channel"`
	APIKey  string `json:"api_key"`
}

type TermiiSMSResponse struct {
	MessageID string `json:"message_id"`
	Message   string `json:"message"`
	Balance   string `json:"balance"`
	User      string `json:"user"`
}

type SMSDispatcher interface {
	SendWaybillCreatedAlert(ctx context.Context, receiverPhone, waybillID, rawPin string, totalFreightKobo int64) error
	SendDriverDispatchAlert(ctx context.Context, driverPhone, waybillID, destinationPark string) error
}

type TermiiSMSDispatcher struct {
	httpClient *http.Client
	baseURL    string
	senderID   string
}

func NewTermiiSMSDispatcher(timeout time.Duration) *TermiiSMSDispatcher {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	sender := os.Getenv("TERMII_SENDER_ID")
	if sender == "" {
		sender = "NURTW-ESC"
	}
	return &TermiiSMSDispatcher{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    "https://api.ng.termii.com/api/sms/send",
		senderID:   sender,
	}
}

func (s *TermiiSMSDispatcher) sendSMS(ctx context.Context, destinationPhone, messageText string) error {
	apiKey := os.Getenv("TERMII_API_KEY")
	if apiKey == "" {
		// Log warning in production if unset, but enforce strict check
		return ErrMissingSMSConfig
	}

	payload := TermiiSMSRequest{
		To:      destinationPhone,
		From:    s.senderID,
		Sms:     messageText,
		Type:    "plain",
		Channel: "generic", // Routes through DND bypass
		APIKey:  apiKey,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal SMS payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to build SMS request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSMSDispatchFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: gateway returned status %d", ErrSMSDispatchFailed, resp.StatusCode)
	}

	var termiiResp TermiiSMSResponse
	if err := json.NewDecoder(resp.Body).Decode(&termiiResp); err != nil {
		return fmt.Errorf("%w: invalid response structure", ErrSMSDispatchFailed)
	}

	return nil
}

func (s *TermiiSMSDispatcher) SendWaybillCreatedAlert(
	ctx context.Context,
	receiverPhone string,
	waybillID string,
	rawPin string,
	totalFreightKobo int64,
) error {
	amountNaira := float64(totalFreightKobo) / 100.0
	msg := fmt.Sprintf(
		"NURTW ESCROW: Waybill %s has been registered. Freight: N%.2f. Your pickup PIN is [%s]. DO NOT share this PIN until you inspect parcel at destination park. Release via *347*%s*%s#",
		waybillID, amountNaira, rawPin, waybillID, rawPin,
	)
	return s.sendSMS(ctx, receiverPhone, msg)
}

func (s *TermiiSMSDispatcher) SendDriverDispatchAlert(
	ctx context.Context,
	driverPhone string,
	waybillID string,
	destinationPark string,
) error {
	msg := fmt.Sprintf(
		"NURTW ESCROW: You have custody of Waybill %s heading to %s. Payout will land instantly in your bank account once receiver verifies at destination counter.",
		waybillID, destinationPark,
	)
	return s.sendSMS(ctx, driverPhone, msg)
}
