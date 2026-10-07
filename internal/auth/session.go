package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

var errBadToken = errors.New("invalid token")

// Signer seals small JSON payloads into tamper-proof cookie values
// (base64url(payload) + "." + base64url(HMAC-SHA256)). The payload is signed,
// not encrypted, so it must hold nothing secret.
type Signer struct{ key []byte }

func NewSigner(secret string) *Signer { return &Signer{key: []byte(secret)} }

// RandomSecret returns a fresh 32-byte secret for deployments that set none.
func RandomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Signer) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

func (s *Signer) Seal(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(raw)
	return p + "." + base64.RawURLEncoding.EncodeToString(s.mac(p)), nil
}

func (s *Signer) Open(token string, v any) error {
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return errBadToken
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(p)) {
		return errBadToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return errBadToken
	}
	return json.Unmarshal(raw, v)
}
