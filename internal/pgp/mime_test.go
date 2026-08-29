package pgp

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func TestEncryptMIMERoundTrip(t *testing.T) {
	pubArmor, privArmor, err := GenerateKeyPair("frank@example.com", "hunter2hunter2")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	pubEntity, err := ParsePublicKey(pubArmor)
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}

	raw := []byte(
		"Message-ID: <1.frank.example.com@example.com>\r\n" +
			"From: Frank <frank@example.com>\r\n" +
			"To: grace@example.com\r\n" +
			"Subject: Secret\r\n" +
			"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"Content-Transfer-Encoding: quoted-printable\r\n" +
			"\r\n" +
			"Hello, Grace! This is secret.\r\n")

	encryptedMsg, err := EncryptMIME(raw, []*openpgp.Entity{pubEntity})
	if err != nil {
		t.Fatalf("EncryptMIME: %v", err)
	}

	msg, err := mail.ReadMessage(bytes.NewReader(encryptedMsg))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v", err)
	}
	if got := msg.Header.Get("Subject"); got != "Secret" {
		t.Errorf("Subject header = %q, want %q (top-level headers must survive encryption)", got, "Secret")
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}
	if mediaType != "multipart/encrypted" {
		t.Fatalf("Content-Type = %q, want multipart/encrypted", mediaType)
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])
	if _, err := mr.NextPart(); err != nil { // control part: application/pgp-encrypted, Version: 1
		t.Fatalf("first part: %v", err)
	}
	part2, err := mr.NextPart()
	if err != nil {
		t.Fatalf("second part: %v", err)
	}
	armored, err := io.ReadAll(part2)
	if err != nil {
		t.Fatalf("read second part: %v", err)
	}

	privEntity, err := ParsePrivateKey(privArmor)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	if err := UnlockPrivateKey(privEntity, "hunter2hunter2"); err != nil {
		t.Fatalf("UnlockPrivateKey: %v", err)
	}

	decrypted, err := DecryptEntity(armored, privEntity)
	if err != nil {
		t.Fatalf("DecryptEntity: %v", err)
	}
	want := "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nHello, Grace! This is secret.\r\n"
	if string(decrypted) != want {
		t.Errorf("DecryptEntity() = %q, want %q", decrypted, want)
	}
}
