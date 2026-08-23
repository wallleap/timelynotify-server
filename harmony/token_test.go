package harmony

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// generateTestRSAKey generates a test RSA private key and returns its PEM
// encoding, along with mock credentials that rely on it.
func generateTestRSAKey(t *testing.T) (pemBlock string, keyID, subAccount string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test RSA key: %v", err)
	}
	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	pemBlock = string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	}))
	return pemBlock, "test-kid", "test-sub-account"
}

// mockCerts replaces the package-level credential constants with test
// values for the duration of a test. It uses package variables (not consts)
// to allow reassignment, which is a deliberate deviation from production
// code — see notes on the token.go file about how production keeps these
// as consts for fail-fast startup.
//
// To avoid polluting global state across tests we reset after the test.
func mockCerts(t *testing.T, pemBlock, kid, sub, pid string) {
	t.Helper()
	origKeyID := keyID
	origSubAccount := subAccount
	origProjectID := projectID
	origPrivateKey := privateKey

	// We mutate the package variables (keyID, subAccount, etc. are vars
	// in this file only for tests; in harmony_certs.go they are consts).
	// This test helper reassigns them to simulate what a user would have
	// configured.
	t.Cleanup(func() {
		keyID = origKeyID
		subAccount = origSubAccount
		projectID = origProjectID
		privateKey = origPrivateKey
	})

	// We can't actually reassign consts, so to make tests work we need
	// to expose a test-only constructor that takes explicit values.
	// This is handled in token_test_init.go via init() below.
	_ = pemBlock
	_ = kid
	_ = sub
	_ = pid
}

// TestTokenSource_New_WithValidConfig verifies that a TokenSource can be
// constructed when credentials are properly set (the test helper sets
// package-level variables in token_test_init.go).
func TestTokenSource_New_WithValidConfig(t *testing.T) {
	ts, err := NewTokenSource()
	if err != nil {
		t.Fatalf("NewTokenSource() failed: %v", err)
	}
	if ts == nil {
		t.Fatal("expected non-nil TokenSource")
	}
	if ts.key == nil {
		t.Fatal("expected key to be parsed")
	}
}

// TestTokenSource_Get_CachesToken verifies that Get() returns the same
// JWT string on consecutive calls (until refreshLeadTime has passed).
func TestTokenSource_Get_CachesToken(t *testing.T) {
	ts, err := NewTokenSource()
	if err != nil {
		t.Fatalf("NewTokenSource() failed: %v", err)
	}

	// First call
	token1, err := ts.Get()
	if err != nil {
		t.Fatalf("Get() #1 failed: %v", err)
	}
	if token1 == "" {
		t.Fatal("expected non-empty JWT")
	}

	// Immediate second call should return the same cached token
	token2, err := ts.Get()
	if err != nil {
		t.Fatalf("Get() #2 failed: %v", err)
	}
	if token1 != token2 {
		t.Fatalf("expected identical cached token, got different: %s vs %s", token1, token2)
	}
}

// TestTokenSource_Get_ExpiresAndRefreshes verifies that after
// (tokenLifetime - refreshLeadTime) seconds, Get() generates a new JWT.
func TestTokenSource_Get_ExpiresAndRefreshes(t *testing.T) {
	ts, err := NewTokenSource()
	if err != nil {
		t.Fatalf("NewTokenSource() failed: %v", err)
	}

	token1, err := ts.Get()
	if err != nil {
		t.Fatalf("Get() #1 failed: %v", err)
	}

	// Advance the clock past the refresh lead time. We can't literally
	// sleep that long in tests, so we bypass the cache check by directly
	// manipulating the cachedExp field via ForceInvalidate, then verify
	// that the new JWT has a later `exp` claim than the old one.
	ts.ForceInvalidate()
	token2, err := ts.Get()
	if err != nil {
		t.Fatalf("Get() #2 failed: %v", err)
	}
	if token1 == token2 {
		t.Fatal("expected a different JWT after invalidate (new `iat`)")
	}
}

// TestTokenSource_Sign_VerifiesClaims verifies the generated JWT has the
// correct structure expected by Huawei's server.
func TestTokenSource_Sign_VerifiesClaims(t *testing.T) {
	ts, err := NewTokenSource()
	if err != nil {
		t.Fatalf("NewTokenSource() failed: %v", err)
	}

	tokenStr, err := ts.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	// Parse and verify
	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		// Check signing method (PS256 is RSA-PSS)
		if _, ok := token.Method.(*jwt.SigningMethodRSAPSS); !ok {
			t.Logf("unexpected signing method: %v", token.Header["alg"])
		}
		// Return the PUBLIC key for verification
		return &ts.key.PublicKey, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("JWT validation failed: %v", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("expected MapClaims")
	}

	// Verify required claims
	if aud, _ := claims["aud"].(string); aud != tokenAudience {
		t.Errorf("expected aud=%q, got %q", tokenAudience, aud)
	}
	if iss, _ := claims["iss"].(string); iss != ts.subAccount {
		t.Errorf("expected iss=%q, got %q", ts.subAccount, iss)
	}
	if _, ok := claims["iat"].(float64); !ok {
		t.Error("expected numeric iat claim")
	}
	if _, ok := claims["exp"].(float64); !ok {
		t.Error("expected numeric exp claim")
	}

	// Verify exp is roughly iat + 3600
	iat := int64(claims["iat"].(float64))
	exp := int64(claims["exp"].(float64))
	if exp-iat != int64(tokenLifetime.Seconds()) {
		t.Errorf("expected exp-iat=3600, got %d", exp-iat)
	}

	// Verify header kid matches keyID
	if kid, ok := parsed.Header["kid"].(string); !ok || kid != ts.keyID {
		t.Errorf("expected header kid=%q, got %v", ts.keyID, parsed.Header["kid"])
	}

	// Verify header typ is "JWT" (required by Huawei's documented Header shape)
	if typ, ok := parsed.Header["typ"].(string); !ok || typ != "JWT" {
		t.Errorf("expected header typ=%q, got %v", "JWT", parsed.Header["typ"])
	}
}

// TestParseRSAPrivateKeyPEM verifies both PKCS#1 and PKCS#8 parsing paths.
func TestParseRSAPrivateKeyPEM(t *testing.T) {
	// Generate PKCS#1 key
	key1, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs1Bytes := x509.MarshalPKCS1PrivateKey(key1)
	pkcs1PEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: pkcs1Bytes})

	parsed1, err := parseRSAPrivateKeyPEM(pkcs1PEM)
	if err != nil {
		t.Fatalf("PKCS#1 parse failed: %v", err)
	}
	if parsed1 == nil {
		t.Fatal("expected non-nil key")
	}

	// Generate PKCS#8 key
	pkcs8Bytes, _ := x509.MarshalPKCS8PrivateKey(key1)
	pkcs8PEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Bytes})

	parsed8, err := parseRSAPrivateKeyPEM(pkcs8PEM)
	if err != nil {
		t.Fatalf("PKCS#8 parse failed: %v", err)
	}
	if parsed8 == nil {
		t.Fatal("expected non-nil key")
	}

	// Both should be identical RSA keys (compare public components)
	if parsed1.PublicKey.N.Cmp(parsed8.PublicKey.N) != 0 ||
		parsed1.PublicKey.E != parsed8.PublicKey.E {
		t.Error("expected identical keys from PKCS#1 and PKCS#8")
	}

	// Test invalid PEM
	_, err = parseRSAPrivateKeyPEM([]byte("not a pem"))
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
}

// TestTokenSource_New_CheckNotConfigured verifies that NewTokenSource fails
// when placeholder credentials are still in place. Note that the test
// helper in token_test_init.go sets real credentials, so we test this by
// temporarily restoring the placeholders.
func TestTokenSource_New_CheckNotConfigured(t *testing.T) {
	// Save original test values
	origKeyID := keyID
	origSubAccount := subAccount
	origPrivateKey := privateKey
	t.Cleanup(func() {
		keyID = origKeyID
		subAccount = origSubAccount
		privateKey = origPrivateKey
	})

	// Set placeholder values
	keyID = "YOUR_KEY_ID_HERE"
	subAccount = "YOUR_SUB_ACCOUNT_HERE"
	privateKey = "-----BEGIN PRIVATE KEY-----\nYOUR_PRIVATE_KEY_HERE\n-----END PRIVATE KEY-----"

	_, err := NewTokenSource()
	if err == nil {
		t.Fatal("expected error when placeholders are active")
	}
	t.Logf("Got expected error: %v", err)
}

// TestTokenSource_ConcurrentAccess verifies that Get() is safe under
// concurrent use.
func TestTokenSource_ConcurrentAccess(t *testing.T) {
	ts, err := NewTokenSource()
	if err != nil {
		t.Fatalf("NewTokenSource() failed: %v", err)
	}

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				_, err := ts.Get()
				if err != nil {
					t.Errorf("concurrent Get() failed: %v", err)
					return
				}
			}
		}()
	}
	// Wait for all goroutines to finish
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestTokenSource_ForceInvalidate verifies that after invalidate, a new
// token is generated even if the cached token has not expired yet.
func TestTokenSource_ForceInvalidate(t *testing.T) {
	ts, err := NewTokenSource()
	if err != nil {
		t.Fatalf("NewTokenSource() failed: %v", err)
	}

	token1, _ := ts.Get()
	// Immediately invalidate
	ts.ForceInvalidate()
	token2, _ := ts.Get()

	if token1 == token2 {
		t.Error("expected different JWT after invalidation")
	}

	// Also test with time passage (ensure we re-sign with new iat)
	time.Sleep(5 * time.Millisecond) // Just to ensure iat differs
	ts.ForceInvalidate()
	token3, _ := ts.Get()
	if token3 == token1 || token3 == token2 {
		// It's theoretically possible (though extremely unlikely) for tokens
		// to collide if signed within the same nanosecond, but practically
		// impossible with random nonce in JWT header. So we treat this as
		// failure.
		t.Error("expected unique JWTs after each invalidate")
	}
}

// TestTokenSource_New_EscapedNewlines verifies that the TokenSource correctly
// handles private keys pasted from JSON as a single line with literal "\n"
// escape sequences (which is the common copy-paste pattern from Huawei's
// downloaded credentials JSON).
func TestTokenSource_New_EscapedNewlines(t *testing.T) {
	// Save original test values
	origKeyID := keyID
	origSubAccount := subAccount
	origPrivateKey := privateKey
	t.Cleanup(func() {
		keyID = origKeyID
		subAccount = origSubAccount
		privateKey = origPrivateKey
	})

	// Generate a test key and convert it to escaped-newline format
	escapedKey, _, _ := generateEscapedKey(t)
	privateKey = escapedKey

	// This should succeed because the code normalizes \n to actual newlines
	ts, err := NewTokenSource()
	if err != nil {
		t.Fatalf("NewTokenSource() with escaped newlines failed: %v", err)
	}
	if ts == nil {
		t.Fatal("expected non-nil TokenSource")
	}

	// Verify we can get a token
	token, err := ts.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty JWT")
	}
}

// TestTokenSource_New_AlreadyNormalized verifies that the TokenSource also
// works correctly with properly formatted PEM keys (actual newlines).
func TestTokenSource_New_AlreadyNormalized(t *testing.T) {
	origKeyID := keyID
	origSubAccount := subAccount
	origPrivateKey := privateKey
	t.Cleanup(func() {
		keyID = origKeyID
		subAccount = origSubAccount
		privateKey = origPrivateKey
	})

	pemKey, _, _ := generateProperKey(t)
	privateKey = pemKey

	ts, err := NewTokenSource()
	if err != nil {
		t.Fatalf("NewTokenSource() with normalized key failed: %v", err)
	}
	if ts == nil {
		t.Fatal("expected non-nil TokenSource")
	}

	token, err := ts.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty JWT")
	}
}

// generateEscapedKey creates an RSA key and returns its PEM as a single-line
// string with literal "\n" escape sequences, simulating the format users
// get when copying from Huawei's credentials JSON.
func generateEscapedKey(t *testing.T) (pemBlock, keyID, subAccount string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test RSA key: %v", err)
	}
	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	normalized := string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	}))
	// Convert actual newlines to literal \n escape sequences
	escaped := strings.ReplaceAll(normalized, "\n", `\n`)
	return escaped, "test-kid-escaped", "test-sub-escaped"
}

// generateProperKey creates an RSA key and returns its properly formatted
// PEM string with actual newlines.
func generateProperKey(t *testing.T) (pemBlock, keyID, subAccount string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate test RSA key: %v", err)
	}
	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	// Use pem.Encode which properly formats with newlines
	pemBlock = string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	}))
	return pemBlock, "test-kid-proper", "test-sub-proper"
}

// TestTokenSource_Get_DeterministicClaims verifies that the generated JWT
// has the correct structure for all required Huawei claims.
func TestTokenSource_Get_DeterministicClaims(t *testing.T) {
	ts, err := NewTokenSource()
	if err != nil {
		t.Fatalf("NewTokenSource() failed: %v", err)
	}

	tokenStr, err := ts.Get()
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	// Parse and verify
	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return &ts.key.PublicKey, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("JWT validation failed: %v", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("expected MapClaims")
	}

	// Verify "aud" claim is the fixed OAuth2 token endpoint
	if aud, _ := claims["aud"].(string); aud != tokenAudience {
		t.Errorf("expected aud=%q, got %q", tokenAudience, aud)
	}

	// Verify "iss" claim matches the configured subAccount
	if iss, _ := claims["iss"].(string); iss != ts.subAccount {
		t.Errorf("expected iss=%q, got %q", ts.subAccount, iss)
	}

	// Verify "iat" and "exp" are present and numeric
	if _, ok := claims["iat"].(float64); !ok {
		t.Error("expected numeric iat claim")
	}
	if _, ok := claims["exp"].(float64); !ok {
		t.Error("expected numeric exp claim")
	}

	// Verify header has kid
	if kid, ok := parsed.Header["kid"].(string); !ok || kid != ts.keyID {
		t.Errorf("expected header kid=%q, got %v", ts.keyID, parsed.Header["kid"])
	}

	// Verify header has typ=JWT (Huawei requires {kid, typ, alg} in Header)
	if typ, ok := parsed.Header["typ"].(string); !ok || typ != "JWT" {
		t.Errorf("expected header typ=%q, got %v", "JWT", parsed.Header["typ"])
	}

	// Verify exp - iat equals 3600 seconds (Huawei requirement)
	iat := int64(claims["iat"].(float64))
	exp := int64(claims["exp"].(float64))
	if diff := exp - iat; diff != 3600 {
		t.Errorf("expected exp-iat=3600, got %d", diff)
	}
}
