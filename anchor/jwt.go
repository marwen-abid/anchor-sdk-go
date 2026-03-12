// Package anchor provides JWT authentication for SEP-10 flows using HMAC-SHA256.
package anchor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marwen-abid/anchor-sdk-go"
	"github.com/marwen-abid/anchor-sdk-go/errors"
)

// hmacJWT implements both JWTIssuer and JWTVerifier using HMAC-SHA256.
type hmacJWT struct {
	secret []byte
	issuer string
	expiry time.Duration
}

// NewHMACJWT returns a JWTIssuer and JWTVerifier backed by HMAC-SHA256.
// The same instance implements both interfaces for symmetric key operations.
func NewHMACJWT(secret []byte, issuer string, expiry time.Duration) (anchorsdk.JWTIssuer, anchorsdk.JWTVerifier) {
	jwt := &hmacJWT{
		secret: secret,
		issuer: issuer,
		expiry: expiry,
	}
	return jwt, jwt
}

// jwtHeader represents the JWT header for HMAC-SHA256.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// jwtPayload represents the JWT payload with standard and custom claims.
type jwtPayload struct {
	Sub        string `json:"sub"`            // Subject: Stellar address
	Iss        string `json:"iss"`            // Issuer: Anchor domain
	Iat        int64  `json:"iat"`            // Issued At: Unix timestamp
	Exp        int64  `json:"exp"`            // Expires: Unix timestamp
	AuthMethod string `json:"auth_method"`    // Custom: SEP-10 auth method
	Memo       string `json:"memo,omitempty"` // Custom: Optional memo
}

// Issue creates a JWT token with the given claims.
func (j *hmacJWT) Issue(ctx context.Context, claims anchorsdk.JWTClaims) (string, error) { //nolint:gocritic // hugeParam: JWTClaims matches the JWTIssuer interface signature
	// Build header
	header := jwtHeader{
		Alg: "HS256",
		Typ: "JWT",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", errors.NewAnchorError(errors.JWT_ISSUE_FAILED, "failed to marshal JWT header", err)
	}
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	// Build payload with timestamps
	now := time.Now()
	payload := jwtPayload{
		Sub:        claims.Subject,
		Iss:        j.issuer,
		Iat:        now.Unix(),
		Exp:        now.Add(j.expiry).Unix(),
		AuthMethod: claims.AuthMethod,
		Memo:       claims.Memo,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", errors.NewAnchorError(errors.JWT_ISSUE_FAILED, "failed to marshal JWT payload", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// Create signature: HMAC-SHA256(header.payload, secret)
	message := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, j.secret)
	mac.Write([]byte(message))
	signature := mac.Sum(nil)
	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	// Return complete JWT: header.payload.signature
	token := message + "." + signatureB64
	return token, nil
}

// Verify validates a JWT token and returns the claims.
func (j *hmacJWT) Verify(ctx context.Context, token string) (*anchorsdk.JWTClaims, error) {
	// Split token into parts
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.NewAnchorError(errors.JWT_VERIFICATION_FAILED, "invalid JWT format: expected 3 parts", nil)
	}
	headerB64, payloadB64, signatureB64 := parts[0], parts[1], parts[2]

	// Verify signature
	message := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, j.secret)
	mac.Write([]byte(message))
	expectedSignature := mac.Sum(nil)
	expectedSignatureB64 := base64.RawURLEncoding.EncodeToString(expectedSignature)

	if signatureB64 != expectedSignatureB64 {
		return nil, errors.NewAnchorError(errors.JWT_VERIFICATION_FAILED, "invalid JWT signature", nil)
	}

	// Decode and parse payload
	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, errors.NewAnchorError(errors.JWT_VERIFICATION_FAILED, "failed to decode JWT payload", err)
	}

	var payload jwtPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, errors.NewAnchorError(errors.JWT_VERIFICATION_FAILED, "failed to parse JWT payload", err)
	}

	// Check expiration
	now := time.Now().Unix()
	if payload.Exp <= now {
		return nil, errors.NewAnchorError(errors.JWT_EXPIRED, fmt.Sprintf("token expired at %d (now: %d)", payload.Exp, now), nil)
	}

	// Check issuer
	if payload.Iss != j.issuer {
		return nil, errors.NewAnchorError(errors.JWT_VERIFICATION_FAILED, fmt.Sprintf("invalid issuer: expected %s, got %s", j.issuer, payload.Iss), nil)
	}

	// Convert to JWTClaims
	claims := &anchorsdk.JWTClaims{
		Subject:    payload.Sub,
		Issuer:     payload.Iss,
		IssuedAt:   time.Unix(payload.Iat, 0),
		ExpiresAt:  time.Unix(payload.Exp, 0),
		AuthMethod: payload.AuthMethod,
		Memo:       payload.Memo,
	}

	return claims, nil
}
