package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newKey(t *testing.T, curve elliptic.Curve) (*ecdsa.PrivateKey, string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

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
	key, pub := newKey(t, elliptic.P256())
	other, _ := newKey(t, elliptic.P256())
	v, err := NewVerifier(pub)
	if err != nil {
		t.Fatal(err)
	}
	u, err := v.Verify(sign(t, jwt.SigningMethodES256, key, base()))
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
		"expired":      sign(t, jwt.SigningMethodES256, key, expired),
		"wrong issuer": sign(t, jwt.SigningMethodES256, key, wrongIss),
		"no expiry":    sign(t, jwt.SigningMethodES256, key, noExp),
		"wrong key":    sign(t, jwt.SigningMethodES256, other, base()),
		"alg none":     sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, base()),
		// The public key is public: a token "signed" with it as an HMAC secret must not pass.
		"HS256 with the public key": sign(t, jwt.SigningMethodHS256, []byte(pub), base()),
		"garbage":                   "not.a.token",
	} {
		if _, err := v.Verify(tok); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestNewVerifierKeyFormats(t *testing.T) {
	_, pub := newKey(t, elliptic.P256())
	for name, raw := range map[string]string{
		"PEM":     pub,
		"escaped": strings.ReplaceAll(pub, "\n", `\n`),
		"base64":  base64.StdEncoding.EncodeToString([]byte(pub)),
	} {
		if _, err := NewVerifier(raw); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, p384 := newKey(t, elliptic.P384())
	if _, err := NewVerifier(p384); err == nil {
		t.Error("P-384 key: expected rejection")
	}
	if _, err := NewVerifier("not a key"); err == nil {
		t.Error("garbage: expected rejection")
	}
}
