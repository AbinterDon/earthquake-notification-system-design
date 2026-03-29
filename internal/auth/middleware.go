// Package auth provides JWT-based authentication middleware.
//
// Every HTTP request carries a signed HS256 JWT in the Authorization header:
//
//	Authorization: Bearer <token>
//
// The JWT must contain a "device_token" claim identifying the caller's device.
// Use cmd/tokengen to mint tokens for local development.
package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const deviceTokenKey contextKey = "device_token"

// Middleware validates the JWT and injects the device token into the request
// context. Requests with missing or invalid tokens receive 401.
func Middleware(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		tokenStr := strings.TrimPrefix(raw, "Bearer ")

		tok, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil || !tok.Valid {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		claims, ok := tok.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "invalid claims", http.StatusUnauthorized)
			return
		}
		deviceToken, ok := claims["device_token"].(string)
		if !ok || deviceToken == "" {
			http.Error(w, "missing device_token claim", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), deviceTokenKey, deviceToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// DeviceToken retrieves the authenticated device token from the context.
// Panics if called outside of an authenticated handler (programming error).
func DeviceToken(ctx context.Context) string {
	v, _ := ctx.Value(deviceTokenKey).(string)
	return v
}
