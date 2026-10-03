package security

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const AgentIDKey contextKey = "agent_id"
const IsAdminKey contextKey = "is_admin"

// AuthMiddleware requires a valid Bearer JWT and injects agent_id + is_admin into context.
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, `{"error":"missing or invalid authorization header"}`, http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		claims, err := ParseToken(tokenString)
		if err != nil {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), AgentIDKey, claims.AgentID)
		ctx = context.WithValue(ctx, IsAdminKey, claims.IsAdmin)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func IsAdminFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(IsAdminKey).(bool)
	return v
}

func AgentIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(AgentIDKey).(string)
	return v
}