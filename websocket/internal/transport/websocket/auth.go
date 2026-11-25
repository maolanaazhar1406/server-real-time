package websocket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

var (
	ErrInvalidJWT = errors.New("websocket: invalid jwt token")
	ErrExpiredJWT = errors.New("websocket: expired jwt token")
)

type jwtClaims struct {
	Sub       string `json:"sub"`
	DeviceID  string `json:"device_id"`
	TokenType string `json:"token_type"`
	Exp       int64  `json:"exp"`
}

// NewJWTTokenValidator verifies HS256 JWT access tokens during WebSocket upgrade handshakes.
// If secret is empty, it falls back to JWT_SECRET env or the default development secret.
func NewJWTTokenValidator(secret string) func(token string) (userID string, deviceID string, err error) {
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}
	if secret == "" {
		secret = "pulsert-dev-secret-change-in-prod"
	}
	key := []byte(secret)

	return func(token string) (string, string, error) {
		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			return "", "", ErrInvalidJWT
		}

		signingInput := parts[0] + "." + parts[1]
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(signingInput))
		expectedSig := mac.Sum(nil)

		gotSig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil || !hmac.Equal(gotSig, expectedSig) {
			return "", "", ErrInvalidJWT
		}

		payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", "", ErrInvalidJWT
		}

		var claims jwtClaims
		if err := json.Unmarshal(payloadBytes, &claims); err != nil {
			return "", "", ErrInvalidJWT
		}
		if claims.TokenType != "" && claims.TokenType != "access" {
			return "", "", ErrInvalidJWT
		}
		if claims.Exp > 0 && time.Now().Unix() >= claims.Exp {
			return "", "", ErrExpiredJWT
		}
		return claims.Sub, claims.DeviceID, nil
	}
}
