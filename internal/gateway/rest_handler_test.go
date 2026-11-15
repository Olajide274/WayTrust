package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nurtw/escrow/internal/repository"
	"github.com/nurtw/escrow/internal/security"
	"github.com/nurtw/escrow/internal/service"
)

func TestHandleDisburseAuthorization(t *testing.T) {
	tests := []struct {
		name              string
		agentID           string
		ownerID           string
		isAdmin           bool
		ownerLookupErr    error
		disburseErr       error
		wantStatus        int
		wantError         string
		wantOwnerLookups  int
		wantDisburseCalls int
	}{
		{
			name:              "owner can disburse",
			agentID:           "agent-owner",
			ownerID:           "agent-owner",
			wantStatus:        http.StatusOK,
			wantOwnerLookups:  1,
			wantDisburseCalls: 1,
		},
		{
			name:              "other agent is forbidden without disbursing",
			agentID:           "agent-other",
			ownerID:           "agent-owner",
			wantStatus:        http.StatusForbidden,
			wantError:         "waybill does not belong to this agent",
			wantOwnerLookups:  1,
			wantDisburseCalls: 0,
		},
		{
			name:              "admin can disburse",
			agentID:           "agent-admin",
			isAdmin:           true,
			wantStatus:        http.StatusOK,
			wantOwnerLookups:  0,
			wantDisburseCalls: 1,
		},
		{
			name:              "admin gets not found for unknown waybill",
			agentID:           "agent-admin",
			isAdmin:           true,
			disburseErr:       repository.ErrWaybillNotFound,
			wantStatus:        http.StatusNotFound,
			wantError:         "waybill not found",
			wantOwnerLookups:  0,
			wantDisburseCalls: 1,
		},
		{
			name:              "unknown waybill is hidden from agents",
			agentID:           "agent-other",
			ownerLookupErr:    repository.ErrWaybillNotFound,
			wantStatus:        http.StatusForbidden,
			wantError:         "waybill does not belong to this agent",
			wantOwnerLookups:  1,
			wantDisburseCalls: 0,
		},
		{
			name:              "missing identity is unauthorized",
			wantStatus:        http.StatusUnauthorized,
			wantError:         "authentication required",
			wantOwnerLookups:  0,
			wantDisburseCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ownerLookupCalls := 0
			disburseCalls := 0
			handler := &RESTHandler{
				getWaybillOwner: func(context.Context, string) (string, error) {
					ownerLookupCalls++
					return tt.ownerID, tt.ownerLookupErr
				},
				disbursePayout: func(context.Context, string, string) (*service.DisbursePayoutResult, error) {
					disburseCalls++
					return &service.DisbursePayoutResult{WaybillID: "WB-test"}, tt.disburseErr
				},
			}

			ctx := context.WithValue(context.Background(), security.IsAdminKey, tt.isAdmin)
			if tt.agentID != "" {
				ctx = context.WithValue(ctx, security.AgentIDKey, tt.agentID)
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/waybills/disburse",
				strings.NewReader(`{"waybill_id":"WB-test","idempotency_key":"disburse-WB-test"}`),
			).WithContext(ctx)
			response := httptest.NewRecorder()

			handler.HandleDisburse(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("response status = %d, want %d; body: %s", response.Code, tt.wantStatus, response.Body)
			}
			if ownerLookupCalls != tt.wantOwnerLookups {
				t.Fatalf("owner lookup calls = %d, want %d", ownerLookupCalls, tt.wantOwnerLookups)
			}
			if disburseCalls != tt.wantDisburseCalls {
				t.Fatalf("DisbursePayout calls = %d, want %d", disburseCalls, tt.wantDisburseCalls)
			}
			if tt.wantError != "" {
				var body map[string]string
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("could not decode error response: %v", err)
				}
				if body["error"] != tt.wantError {
					t.Fatalf("error = %q, want %q", body["error"], tt.wantError)
				}
			}
		})
	}
}
