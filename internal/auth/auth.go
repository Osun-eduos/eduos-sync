// Package auth checks access tokens issued by eduos-api. The sync service never issues tokens of its own: it holds
// only eduos-api's public key, so even if this service were compromised it could not sign a token.
package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// User is the signed-in person, as described by their access token.
type User struct {
	ID                 string
	Role               string
	SchoolID           string
	MustChangePassword bool
}

type claims struct {
	Role string  `json:"role"`
	SID  *string `json:"sid"`
	MCP  bool    `json:"mcp"`
	jwt.RegisteredClaims
}

var ErrInvalid = errors.New("your session has expired; please sign in again")

type Verifier struct {
	key    *ecdsa.PublicKey
	parser *jwt.Parser
}

// PEM accepts a PEM block as it is usually put in an environment variable: as is, with "\n" written as two
// characters, or base64-encoded.
func PEM(raw string) []byte {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "-----BEGIN") {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil {
			s = strings.TrimSpace(string(b))
		}
	}
	return []byte(strings.ReplaceAll(s, `\n`, "\n"))
}

// NewVerifier accepts only ES256 tokens from the eduos-api issuer, with an expiry, signed by the private key that
// matches publicKey (a P-256 public key in PEM form).
func NewVerifier(publicKey string) (*Verifier, error) {
	key, err := jwt.ParseECPublicKeyFromPEM(PEM(publicKey))
	if err != nil {
		return nil, fmt.Errorf("JWT_PUBLIC_KEY is not a valid EC public key: %w", err)
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("JWT_PUBLIC_KEY must be a P-256 (ES256) key")
	}
	return &Verifier{
		key:    key,
		parser: jwt.NewParser(jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer("eduos-api"), jwt.WithExpirationRequired()),
	}, nil
}

func (v *Verifier) Verify(token string) (User, error) {
	var c claims
	_, err := v.parser.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return v.key, nil })
	if err != nil || c.Subject == "" || c.Role == "" {
		return User{}, fmt.Errorf("%w", ErrInvalid)
	}
	u := User{ID: c.Subject, Role: c.Role, MustChangePassword: c.MCP}
	if c.SID != nil {
		u.SchoolID = *c.SID
	}
	return u, nil
}
