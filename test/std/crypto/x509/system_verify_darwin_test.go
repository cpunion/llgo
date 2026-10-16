package x509_test

import (
	"crypto/x509"
	"os"
	"testing"
	"time"
)

func TestSystemVerifierRejectsUntrustedCertificate(t *testing.T) {
	// Keep the native verifier enabled even if the surrounding environment
	// requests fallback roots. No network or installed test root is needed.
	t.Setenv("GODEBUG", os.Getenv("GODEBUG")+",x509usefallbackroots=0")
	certificate, _ := generateSelfSignedCert(t)
	_, err := certificate.Verify(x509.VerifyOptions{
		DNSName:     "test.example.com",
		CurrentTime: certificate.NotBefore.Add(time.Hour),
	})
	if _, untrusted := err.(x509.UnknownAuthorityError); !untrusted {
		t.Fatalf("system verification returned %T (%v), want UnknownAuthorityError", err, err)
	}
}
