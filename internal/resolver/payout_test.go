package resolver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInitiateDriverPayoutReturnsPaystackStatus(t *testing.T) {
	for _, status := range []string{"success", "pending", "otp", "failed"} {
		t.Run(status, func(t *testing.T) {
			t.Setenv("PAYSTACK_SECRET_KEY", "test-key")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/transferrecipient":
					_, _ = w.Write([]byte(`{"status":true,"data":{"recipient_code":"RCP_test"}}`))
				case "/transfer":
					_ = json.NewEncoder(w).Encode(map[string]any{
						"status":  true,
						"message": "Transfer attempted",
						"data": map[string]string{
							"reference":        "REF-1",
							"status":           status,
							"gateway_response": "Paystack transfer result",
						},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			disburser := &PaystackPayoutDisburser{
				httpClient: server.Client(),
				baseURL:    server.URL,
			}
			transfer, err := disburser.InitiateDriverPayout(
				context.Background(), "Driver", "0123456789", "001", 1000, "REF-1",
			)
			if err != nil {
				t.Fatalf("InitiateDriverPayout() error = %v", err)
			}
			if transfer.Status != status || transfer.Reference != "REF-1" {
				t.Fatalf("InitiateDriverPayout() = %+v, want status %q and reference REF-1", transfer, status)
			}
			if transfer.HTTPStatus != http.StatusOK || transfer.Message != "Paystack transfer result" {
				t.Fatalf("InitiateDriverPayout() metadata = %+v", transfer)
			}
		})
	}
}

func TestInitiateDriverPayoutVerifiesDuplicateReference(t *testing.T) {
	t.Setenv("PAYSTACK_SECRET_KEY", "test-key")
	var transferCalls, verifyCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/transferrecipient":
			_, _ = w.Write([]byte(`{"status":true,"data":{"recipient_code":"RCP_test"}}`))
		case "/transfer":
			transferCalls++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":false,"message":"Duplicate transfer reference"}`))
		case "/transfer/verify/REF-1":
			verifyCalls++
			_, _ = w.Write([]byte(`{
				"status":true,
				"message":"Transfer retrieved",
				"data":{"reference":"REF-1","status":"success","gateway_response":"Approved"}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	disburser := &PaystackPayoutDisburser{
		httpClient: server.Client(),
		baseURL:    server.URL,
	}
	transfer, err := disburser.InitiateDriverPayout(
		context.Background(), "Driver", "0123456789", "001", 1000, "REF-1",
	)
	if err != nil {
		t.Fatalf("InitiateDriverPayout() error = %v", err)
	}
	if transfer.Reference != "REF-1" || transfer.Status != "success" {
		t.Fatalf("InitiateDriverPayout() = %+v, want verified success for REF-1", transfer)
	}
	if transferCalls != 1 || verifyCalls != 1 {
		t.Fatalf("transfer calls = %d, verify calls = %d, want 1 each", transferCalls, verifyCalls)
	}
}

func TestVerifyTransferReturnsFailureDetails(t *testing.T) {
	t.Setenv("PAYSTACK_SECRET_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transfer/verify/REF-1" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"status":true,
			"message":"Transfer retrieved",
			"data":{"reference":"REF-1","status":"failed","gateway_response":"Bank rejected transfer"}
		}`))
	}))
	defer server.Close()

	disburser := &PaystackPayoutDisburser{
		httpClient: server.Client(),
		baseURL:    server.URL,
	}
	transfer, err := disburser.VerifyTransfer(context.Background(), "REF-1")
	if err != nil {
		t.Fatalf("VerifyTransfer() error = %v", err)
	}
	if transfer.Status != "failed" || transfer.Message != "Bank rejected transfer" {
		t.Fatalf("VerifyTransfer() = %+v, want failed status and Paystack message", transfer)
	}
}
