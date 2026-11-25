// Package auth checks access tokens issued by eduos-api. The sync service never issues tokens of its own.
package auth

import (
	"errors"
	"fmt"

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
	secret []byte
	parser *jwt.Parser
}

// NewVerifier accepts only HS256 tokens from the eduos-api issuer, with an expiry.
func NewVerifier(secret string) *Verifier {
	return &Verifier{
		secret: []byte(secret),
		parser: jwt.NewParser(jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("eduos-api"), jwt.WithExpirationRequired()),
	}
}

func (v *Verifier) Verify(token string) (User, error) {
	var c claims
	_, err := v.parser.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return v.secret, nil })
	if err != nil || c.Subject == "" || c.Role == "" {
		return User{}, fmt.Errorf("%w", ErrInvalid)
	}
	u := User{ID: c.Subject, Role: c.Role, MustChangePassword: c.MCP}
	if c.SID != nil {
		u.SchoolID = *c.SID
	}
	return u, nil
}
