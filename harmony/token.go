package harmony

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// tokenAudience is the fixed "aud" claim required by the Huawei OAuth2 server
// when presenting a service-account JWT.
const tokenAudience = "https://oauth-login.cloud.huawei.com/oauth2/v3/token"

// tokenLifetime is the JWT lifetime accepted by the Huawei server. Per the
// official spec, exp must equal iat + 3600.
const tokenLifetime = 3600 * time.Second

// refreshLeadTime is how far in advance of expiry the cached JWT is
// considered stale, to avoid racing Huawei's clock.
const refreshLeadTime = 5 * time.Minute

// TokenSource produces and caches Huawei Push Kit service-account JWTs.
//
// A zero-value TokenSource is not usable; always construct one via
// NewTokenSource. TokenSource is safe for concurrent use.
type TokenSource struct {
	key        *rsa.PrivateKey
	keyID      string
	subAccount string

	mu        sync.Mutex
	cached    string
	cachedExp time.Time
}

// NewTokenSource builds a TokenSource from the package-level credential
// variables defined in harmony_certs.go. It returns an error if the
// private key cannot be parsed or if the credentials still contain the
// placeholder values.
// Note: keyID, subAccount, privateKey, projectID are variables (not
// constants) to allow test injection and runtime reassignment.
func NewTokenSource() (*TokenSource, error) {
	if keyID == "YOUR_KEY_ID_HERE" || subAccount == "YOUR_SUB_ACCOUNT_HERE" {
		return nil, errors.New("harmony service-account credentials not configured: edit harmony/harmony_certs.go first")
	}
	if strings.Contains(privateKey, "YOUR_PRIVATE_KEY_HERE") {
		return nil, errors.New("harmony private key not configured: edit harmony/harmony_certs.go first")
	}

	// Normalize the private key:
	// Users often paste the private key from JSON as a single line with
	// literal "\n" escape sequences (e.g. "-----BEGIN PRIVATE KEY-----\nMIIJ...").
	// We need to convert these to actual newlines for PEM decoding.
	normalizedKey := strings.ReplaceAll(privateKey, `\n`, "\n")

	key, err := parseRSAPrivateKeyPEM([]byte(normalizedKey))
	if err != nil {
		return nil, fmt.Errorf("parse harmony private key: %w", err)
	}
	return &TokenSource{
		key:        key,
		keyID:      keyID,
		subAccount: subAccount,
	}, nil
}

// parseRSAPrivateKeyPEM decodes a PEM-encoded PKCS#8 or PKCS#1 RSA private
// key. Huawei's downloaded JSON uses PKCS#8, but we accept both to be
// resilient to user-provided keys.
func parseRSAPrivateKeyPEM(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("PEM decode failed")
	}
	if got := block.Type; got != "PRIVATE KEY" && got != "RSA PRIVATE KEY" {
		return nil, fmt.Errorf("unexpected PEM type %q", got)
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("PKCS#8 key is not RSA")
		}
		return rsaKey, nil
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA private key: %w", err)
	}
	return key, nil
}

// Get returns a valid (cached or freshly generated) JWT bearer token.
// It refreshes the cache when the cached token is missing or will expire
// within refreshLeadTime.
func (ts *TokenSource) Get() (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	now := time.Now()
	if ts.cached != "" && now.Add(refreshLeadTime).Before(ts.cachedExp) {
		return ts.cached, nil
	}
	token, exp, err := ts.sign(now)
	if err != nil {
		return "", err
	}
	ts.cached = token
	ts.cachedExp = exp
	return token, nil
}

// ForceInvalidate causes the next Get call to regenerate a JWT regardless
// of the cached expiry. Use this after receiving a Huawei-side 401 /
// token-expired error (e.g. 80200003) to recover from clock drift or a
// server-side token rotation.
func (ts *TokenSource) ForceInvalidate() {
	ts.mu.Lock()
	ts.cached = ""
	ts.cachedExp = time.Time{}
	ts.mu.Unlock()
}

// sign creates a new JWT valid for tokenLifetime starting at iat.
func (ts *TokenSource) sign(iat time.Time) (token string, exp time.Time, err error) {
	exp = iat.Add(tokenLifetime)
	claims := jwt.MapClaims{
		"aud": tokenAudience,
		"iss": ts.subAccount,
		"iat": iat.Unix(),
		"exp": exp.Unix(),
	}
	jw := jwt.NewWithClaims(jwt.SigningMethodPS256, claims)
	jw.Header["kid"] = ts.keyID
	signed, err := jw.SignedString(ts.key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign harmony JWT: %w", err)
	}
	return signed, exp, nil
}

// Ensure crypto/rand is wired at package init; referenced for clarity
// since the jwt library pulls it in via crypto signing.
var _ = rand.Reader

// Ensure the RSA key is a crypto.Signer (used by jwt/v4).
var _ crypto.Signer = (*rsa.PrivateKey)(nil)
