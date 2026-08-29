package pgp

import (
	"bytes"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func TestGenerateEncryptDecryptRoundTrip(t *testing.T) {
	pubArmor, privArmor, err := GenerateKeyPair("carol@example.com", "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	pubEntity, err := ParsePublicKey(pubArmor)
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}

	raw := []byte("the secret message body")
	encrypted, err := EncryptEntity(raw, []*openpgp.Entity{pubEntity})
	if err != nil {
		t.Fatalf("EncryptEntity: %v", err)
	}

	privEntity, err := ParsePrivateKey(privArmor)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	if !privEntity.PrivateKey.Encrypted {
		t.Fatal("private key should be Encrypted (passphrase-protected) before unlocking")
	}

	// Wrong passphrase must fail.
	if err := UnlockPrivateKey(privEntity, "wrong-passphrase"); err == nil {
		t.Error("UnlockPrivateKey succeeded with wrong passphrase, want error")
	}

	if err := UnlockPrivateKey(privEntity, "correct-horse-battery-staple"); err != nil {
		t.Fatalf("UnlockPrivateKey: %v", err)
	}

	decrypted, err := DecryptEntity(encrypted, privEntity)
	if err != nil {
		t.Fatalf("DecryptEntity: %v", err)
	}
	if !bytes.Equal(decrypted, raw) {
		t.Errorf("DecryptEntity() = %q, want %q", decrypted, raw)
	}
}

func TestCache(t *testing.T) {
	c := NewCache()
	if _, ok := c.Get("tok1", 1); ok {
		t.Fatal("expected empty cache miss")
	}
	e := &openpgp.Entity{}
	c.Put("tok1", 1, e)
	got, ok := c.Get("tok1", 1)
	if !ok || got != e {
		t.Fatal("expected cache hit for tok1/1")
	}
	c.ClearSession("tok1")
	if _, ok := c.Get("tok1", 1); ok {
		t.Fatal("expected cache miss after ClearSession")
	}
}
