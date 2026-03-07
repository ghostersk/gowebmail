// Package mfa provides TOTP-based two-factor authentication (RFC 6238).
// Compatible with Google Authenticator, Authy, and any standard TOTP app.
// No external dependencies — uses only the Go standard library.
package mfa

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net/url"
	"strings"
	"time"
)

const (
	totpDigits = 6
	totpPeriod = 30 // seconds
	totpWindow = 2  // accept ±2 periods (±60s) to handle clock skew and slow input
)

// GenerateSecret creates a new random 20-byte (160-bit) TOTP secret,
// returned as a base32-encoded string (the standard format for authenticator apps).
func GenerateSecret() (string, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// OTPAuthURL builds an otpauth:// URI for QR code generation.
// issuer is the application name (e.g. "GoMail"), accountName is the user's email.
func OTPAuthURL(issuer, accountName, secret string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprintf("%d", totpDigits))
	v.Set("period", fmt.Sprintf("%d", totpPeriod))

	label := url.PathEscape(issuer + ":" + accountName)
	return fmt.Sprintf("otpauth://totp/%s?%s", label, v.Encode())
}

// QRCodeURL returns a Google Charts URL that renders the otpauth URI as a QR code.
// In production you'd generate this server-side; this is convenient for self-hosted use.
func QRCodeURL(issuer, accountName, secret string) string {
	otpURL := OTPAuthURL(issuer, accountName, secret)
	return fmt.Sprintf(
		"https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=%s",
		url.QueryEscape(otpURL),
	)
}

// Validate checks whether code is a valid TOTP code for secret at the current time.
// It accepts codes from [now-window*period, now+window*period] to handle clock skew.
// Handles both padded and unpadded base32 secrets.
func Validate(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	// Normalise: uppercase, strip spaces and padding, then re-decode.
	// Accept both padded (JBSWY3DP====) and unpadded (JBSWY3DP) base32.
	cleaned := strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	cleaned = strings.TrimRight(cleaned, "=")
	keyBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleaned)
	if err != nil {
		log.Printf("mfa: base32 decode error (secret len=%d): %v", len(secret), err)
		return false
	}
	now := time.Now().Unix()
	counter := now / totpPeriod
	for delta := int64(-totpWindow); delta <= int64(totpWindow); delta++ {
		if totp(keyBytes, counter+delta) == code {
			return true
		}
	}
	return false
}

// totp computes a 6-digit TOTP for the given key and counter (RFC 6238 / HOTP RFC 4226).
func totp(key []byte, counter int64) string {
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, uint64(counter))

	mac := hmac.New(sha1.New, key)
	mac.Write(msg)
	h := mac.Sum(nil)

	// Dynamic truncation
	offset := h[len(h)-1] & 0x0f
	code := (int64(h[offset]&0x7f) << 24) |
		(int64(h[offset+1]) << 16) |
		(int64(h[offset+2]) << 8) |
		int64(h[offset+3])

	otp := code % int64(math.Pow10(totpDigits))
	return fmt.Sprintf("%0*d", totpDigits, otp)
}
