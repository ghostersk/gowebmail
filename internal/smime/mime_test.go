package smime

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"testing"
)

func TestSignMIMERoundTrip(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSigned("dave@example.com", DefaultValidity)
	if err != nil {
		t.Fatalf("GenerateSelfSigned: %v", err)
	}

	// CTE deliberately "7bit", not "quoted-printable": Go's mime/multipart.Part.Read
	// auto-decodes quoted-printable/base64 parts, which would make this test compare
	// decoded bytes against the raw wire bytes that were actually signed — a test-harness
	// footgun, not a production concern (a spec-compliant S/MIME verifier signs/checks the
	// encoded wire octets, never the decoded form).
	raw := []byte(
		"Message-ID: <1.dave.example.com@example.com>\r\n" +
			"From: Dave <dave@example.com>\r\n" +
			"To: eve@example.com\r\n" +
			"Subject: Hello\r\n" +
			"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"Content-Transfer-Encoding: 7bit\r\n" +
			"\r\n" +
			"Hello, Eve!\r\n")

	signed, err := SignMIME(certPEM, keyPEM, raw)
	if err != nil {
		t.Fatalf("SignMIME: %v", err)
	}

	// Parse it back like a real mail client would: read top-level headers, find the
	// multipart/signed boundary, split into the two parts, and verify.
	msg, err := mail.ReadMessage(bytes.NewReader(signed))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v", err)
	}
	if got := msg.Header.Get("Subject"); got != "Hello" {
		t.Errorf("Subject header = %q, want %q (top-level headers must survive signing)", got, "Hello")
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}
	if mediaType != "multipart/signed" {
		t.Fatalf("Content-Type = %q, want multipart/signed", mediaType)
	}

	mr := multipart.NewReader(msg.Body, params["boundary"])
	part1, err := mr.NextPart()
	if err != nil {
		t.Fatalf("first part: %v", err)
	}
	part1Headers := "Content-Type: " + part1.Header.Get("Content-Type") + "\r\n"
	if cte := part1.Header.Get("Content-Transfer-Encoding"); cte != "" {
		part1Headers += "Content-Transfer-Encoding: " + cte + "\r\n"
	}
	part1Body, err := io.ReadAll(part1)
	if err != nil {
		t.Fatalf("read first part: %v", err)
	}
	entity := append([]byte(part1Headers+"\r\n"), part1Body...)

	part2, err := mr.NextPart()
	if err != nil {
		t.Fatalf("second part: %v", err)
	}
	sigB64, err := io.ReadAll(part2)
	if err != nil {
		t.Fatalf("read second part: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(sigB64)))
	if err != nil {
		t.Fatalf("decode signature base64: %v", err)
	}

	signer, err := VerifySigned(entity, sig)
	if err != nil {
		t.Fatalf("VerifySigned: %v", err)
	}
	if signer.EmailAddresses[0] != "dave@example.com" {
		t.Errorf("signer = %v, want dave@example.com", signer.EmailAddresses)
	}
}
