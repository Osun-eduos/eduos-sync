package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const secret = "test-secret-test-secret-test-secret-123"

func sign(t *testing.T, method jwt.SigningMethod, key any, c jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, c).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func base() jwt.MapClaims {
	return jwt.MapClaims{"sub": "u1", "role": "TEACHER", "sid": "s1", "mcp": false, "iss": "eduos-api", "exp": time.Now().Add(time.Minute).Unix()}
}

func TestVerify(t *testing.T) {
	v := NewVerifier(secret)
	u, err := v.Verify(sign(t, jwt.SigningMethodHS256, []byte(secret), base()))
	if err != nil || u.ID != "u1" || u.Role != "TEACHER" || u.SchoolID != "s1" || u.MustChangePassword {
		t.Fatalf("valid token: %+v %v", u, err)
	}

	expired := base()
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	wrongIss := base()
	wrongIss["iss"] = "someone-else"
	noExp := base()
	delete(noExp, "exp")
	for name, tok := range map[string]string{
		"expired":      sign(t, jwt.SigningMethodHS256, []byte(secret), expired),
		"wrong issuer": sign(t, jwt.SigningMethodHS256, []byte(secret), wrongIss),
		"no expiry":    sign(t, jwt.SigningMethodHS256, []byte(secret), noExp),
		"wrong secret": sign(t, jwt.SigningMethodHS256, []byte("another-secret-another-secret-12345"), base()),
		"alg none":     sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, base()),
		"HS512":        sign(t, jwt.SigningMethodHS512, []byte(secret), base()),
		"garbage":      "not.a.token",
	} {
		if _, err := v.Verify(tok); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}
