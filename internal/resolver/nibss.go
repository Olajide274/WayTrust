package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidNubanPayload = errors.New("invalid response payload from NIBSS resolution switch")
	ErrNibssUnreachable    = errors.New("NIBSS switch unreachable or rejected account/bank combination")
	ErrMissingSecretKey    = errors.New("payment gateway API key is not configured in environment")
)

// PaystackResolveResponse represents the raw NIBSS resolution response envelope
type PaystackResolveResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		AccountNumber string `json:"account_number"`
		AccountName   string `json:"account_name"`
		BankID        int    `json:"bank_id"`
	} `json:"data"`
}

// VerificationResult contains the resolution outcome and calculated similarity metric
type VerificationResult struct {
	ResolvedAccountName string  `json:"resolved_account_name"`
	ExpectedName        string  `json:"expected_name"`
	ConfidenceScore     float64 `json:"confidence_score"`
	PassedThreshold     bool    `json:"passed_threshold"`
}

// BankAccountResolver defines the interface for verifying driver NUBANs
type BankAccountResolver interface {
	ResolveAndVerify(ctx context.Context, accountNo, bankCode, expectedName string, minConfidence float64) (*VerificationResult, error)
}

// PaystackNibssResolver handles NIBSS verification via Paystack's API
type PaystackNibssResolver struct {
	httpClient *http.Client
	baseURL    string
}

// NewPaystackNibssResolver constructs the resolver with a secure HTTP client and sensible timeouts
func NewPaystackNibssResolver(timeout time.Duration) *PaystackNibssResolver {
	if timeout <= 0 {
		timeout = 7 * time.Second
	}
	return &PaystackNibssResolver{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		baseURL: "https://api.paystack.co",
	}
}

// min returns the smallest of two integers
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// LevenshteinDistance computes the edit distance using dynamic programming
func LevenshteinDistance(s1, s2 string) int {
	len1 := len(s1)
	len2 := len(s2)

	matrix := make([][]int, len1+1)
	for i := range matrix {
		matrix[i] = make([]int, len2+1)
	}

	for i := 0; i <= len1; i++ {
		matrix[i][0] = i
	}
	for j := 0; j <= len2; j++ {
		matrix[0][j] = j
	}

	for i := 1; i <= len1; i++ {
		for j := 1; j <= len2; j++ {
			cost := 0
			if s1[i-1] != s2[j-1] {
				cost = 1
			}
			matrix[i][j] = minInt(
				matrix[i-1][j]+1, // deletion
				minInt(
					matrix[i][j-1]+1,      // insertion
					matrix[i-1][j-1]+cost, // substitution
				),
			)
		}
	}

	return matrix[len1][len2]
}

// TokenizeAndNormalize strips extraneous whitespace, converts to uppercase, and sorts tokens alphabetically
// Example: "Usman Ibrahim" -> "IBRAHIM USMAN", matching "Ibrahim Usman"
func TokenizeAndNormalize(name string) string {
	cleaned := strings.ToUpper(strings.TrimSpace(name))
	tokens := strings.Fields(cleaned)
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

// CalculateSimilarity produces a normalized score between 0.00 and 1.00
func CalculateSimilarity(name1, name2 string) float64 {
	norm1 := TokenizeAndNormalize(name1)
	norm2 := TokenizeAndNormalize(name2)

	if norm1 == norm2 {
		return 1.0
	}

	maxLen := len(norm1)
	if len(norm2) > maxLen {
		maxLen = len(norm2)
	}

	if maxLen == 0 {
		return 1.0
	}

	dist := LevenshteinDistance(norm1, norm2)
	similarity := 1.0 - (float64(dist) / float64(maxLen))
	if similarity < 0.0 {
		return 0.0
	}
	return similarity
}

// ResolveAndVerify contacts NIBSS switch, parses response, and evaluates string similarity
func (r *PaystackNibssResolver) ResolveAndVerify(
	ctx context.Context,
	accountNo string,
	bankCode string,
	expectedName string,
	minConfidence float64,
) (*VerificationResult, error) {
	apiKey := os.Getenv("PAYSTACK_SECRET_KEY")
	if apiKey == "" {
		return nil, ErrMissingSecretKey
	}

	endpoint := fmt.Sprintf("%s/bank/resolve?account_number=%s&bank_code=%s", r.baseURL, accountNo, bankCode)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build NIBSS HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNibssUnreachable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP status %d", ErrNibssUnreachable, resp.StatusCode)
	}

	var envelope PaystackResolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidNubanPayload, err)
	}

	if !envelope.Status || strings.TrimSpace(envelope.Data.AccountName) == "" {
		return nil, ErrInvalidNubanPayload
	}

	resolvedName := envelope.Data.AccountName
	confidence := CalculateSimilarity(resolvedName, expectedName)

	result := &VerificationResult{
		ResolvedAccountName: resolvedName,
		ExpectedName:        expectedName,
		ConfidenceScore:     confidence,
		PassedThreshold:     confidence >= minConfidence,
	}

	if !result.PassedThreshold {
		return result, fmt.Errorf(
			"NAME_MISMATCH: Resolved bank name '%s' does not match declared name '%s' (Confidence: %.2f, Min Required: %.2f)",
			resolvedName, expectedName, confidence, minConfidence,
		)
	}

	return result, nil
}
