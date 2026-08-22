package harmony

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
)

// init for tests only: this file is compiled only when running `go test`.
// It overrides the package-level credentials with a freshly generated
// RSA key pair so that tests never touch real production keys.
func init() {
	// Generate a test RSA key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("failed to generate test RSA key for harmony tests")
	}

	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	pemBlock := string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	}))

	// Override the variables in harmony_certs.go with test values.
	// Because they are now variables (not consts), we can assign to them.
	keyID = "test-kid-" + string(randStr(8))
	subAccount = "test-sub-account"
	projectID = "test-project-id"
	privateKey = pemBlock
}

// randStr generates a short random hex string for the test kid.
// (Not cryptographically important; just avoids collisions between tests.)
func randStr(n int) []byte {
	const hex = "0123456789abcdef"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	for i := range buf {
		buf[i] = hex[int(buf[i])%len(hex)]
	}
	return buf
}
