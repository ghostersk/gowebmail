package smime

import (
	"bytes"
	"crypto/x509"
	"testing"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSigned("alice@example.com", DefaultValidity)
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	raw := []byte("this is the raw MIME message body")
	sig, err := Sign(certPEM, keyPEM, raw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	signer, err := VerifySigned(raw, sig)
	if err != nil {
		t.Fatalf("VerifySigned: %v", err)
	}
	if len(signer.EmailAddresses) == 0 || signer.EmailAddresses[0] != "alice@example.com" {
		t.Errorf("signer email = %v, want [alice@example.com]", signer.EmailAddresses)
	}

	// Tamper one byte of the content — verification must fail.
	tampered := bytes.Clone(raw)
	tampered[0] ^= 0xFF
	if _, err := VerifySigned(tampered, sig); err == nil {
		t.Error("VerifySigned succeeded against tampered content, want error")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSigned("bob@example.com", DefaultValidity)
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}
	cert, err := ParseCertPEM(certPEM)
	if err != nil {
		t.Fatalf("ParseCertPEM: %v", err)
	}

	raw := []byte("secret message body")
	enveloped, err := Encrypt(raw, []*x509.Certificate{cert})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	decrypted, err := Decrypt(enveloped, certPEM, keyPEM)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, raw) {
		t.Errorf("Decrypt() = %q, want %q", decrypted, raw)
	}
}
