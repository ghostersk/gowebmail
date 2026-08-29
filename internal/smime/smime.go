// Package smime provides S/MIME certificate generation, signing, and encryption
// for outgoing mail (RFC 8551, via detached CMS/PKCS#7).
//
// Posture note: this package is certificate-chain-agnostic — it verifies that a CMS
// signature matches the given certificate, not that the certificate is trusted by any
// PKI. "Verified" means "signed with the key matching this cert," nothing more. Callers
// that want a "known sender" UI hint should compare against the user's own S/MIME
// contact address book, not treat a successful Verify as proof of identity.
package smime

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"go.mozilla.org/pkcs7"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

func init() {
	// The pkcs7 library defaults to legacy DES-CBC; use AES-256-GCM instead.
	pkcs7.ContentEncryptionAlgorithm = pkcs7.EncryptionAlgorithmAES256GCM
}

// DefaultValidity is the lifetime used for a freshly self-signed identity.
const DefaultValidity = 365 * 24 * time.Hour

// GenerateSelfSigned creates a new RSA-2048 self-signed S/MIME identity for email.
func GenerateSelfSigned(email string, validity time.Duration) (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: email},
		EmailAddresses:        []string{email},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// ImportPKCS12 extracts a cert+key pair from a .p12/.pfx bundle. RSA keys only —
// the pkcs7 library used for signing/encrypting can't drive an EC key here.
func ImportPKCS12(data []byte, password string) (certPEM, keyPEM []byte, err error) {
	key, cert, err := pkcs12.Decode(data, password)
	if err != nil {
		return nil, nil, fmt.Errorf("decode p12: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("only RSA keys are supported for S/MIME import")
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// ParseCertPEM decodes a PEM-encoded X.509 certificate.
func ParseCertPEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("invalid certificate PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

// ParseKeyPEM decodes a PEM-encoded private key, trying PKCS#8 then falling back to PKCS#1.
func ParseKeyPEM(keyPEM []byte) (crypto.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("invalid key PEM")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return key, nil
}

// Sign produces a detached CMS/PKCS#7 signature (RFC 8551) over raw, using SHA-256.
func Sign(certPEM, keyPEM, raw []byte) ([]byte, error) {
	cert, err := ParseCertPEM(certPEM)
	if err != nil {
		return nil, err
	}
	key, err := ParseKeyPEM(keyPEM)
	if err != nil {
		return nil, err
	}
	sd, err := pkcs7.NewSignedData(raw)
	if err != nil {
		return nil, fmt.Errorf("new signed data: %w", err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSigner(cert, key, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, fmt.Errorf("add signer: %w", err)
	}
	sd.Detach()
	return sd.Finish()
}

// VerifySigned checks a detached signature against the original content and returns the
// signer's certificate. It does NOT validate the certificate against any trust store —
// see the package doc comment.
func VerifySigned(raw, signature []byte) (*x509.Certificate, error) {
	p7, err := pkcs7.Parse(signature)
	if err != nil {
		return nil, fmt.Errorf("parse signature: %w", err)
	}
	p7.Content = raw
	if err := p7.Verify(); err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	signer := p7.GetOnlySigner()
	if signer == nil {
		return nil, errors.New("no signer certificate found in signature")
	}
	return signer, nil
}

// Encrypt wraps raw in a PKCS#7 enveloped-data structure (application/pkcs7-mime,
// smime-type=enveloped-data) for the given recipient certificates.
func Encrypt(raw []byte, recipients []*x509.Certificate) ([]byte, error) {
	return pkcs7.Encrypt(raw, recipients)
}

// Decrypt opens a PKCS#7 enveloped-data structure using the given identity's cert/key.
func Decrypt(enveloped, certPEM, keyPEM []byte) ([]byte, error) {
	cert, err := ParseCertPEM(certPEM)
	if err != nil {
		return nil, err
	}
	key, err := ParseKeyPEM(keyPEM)
	if err != nil {
		return nil, err
	}
	p7, err := pkcs7.Parse(enveloped)
	if err != nil {
		return nil, fmt.Errorf("parse enveloped data: %w", err)
	}
	return p7.Decrypt(cert, key)
}

// ---- Whole-message MIME wrapping (RFC 8551 multipart/signed) ----
//
// SignMIME/verifies operate on a *complete* raw RFC 5322 message (headers + body, as
// produced by internal/email's buildMIMEMessage) rather than a bare payload — Sign/Verify
// above only handle the CMS blob itself.

// SignMIME wraps a complete raw MIME message in a multipart/signed structure: the
// original message's Content-Type + body become the first part, and a detached CMS
// signature over that part becomes the second. All other top-level headers (From, To,
// Subject, Date, Message-ID, ...) are preserved unchanged.
func SignMIME(certPEM, keyPEM, raw []byte) ([]byte, error) {
	topLines, entity, err := splitMIMEEntity(raw)
	if err != nil {
		return nil, err
	}
	// Per RFC 1847 §2.1, the CRLF immediately preceding the boundary delimiter is part of
	// the delimiter, not the signed content — a compliant multipart parser hands back the
	// part body WITHOUT it. Sign the same bytes a parser will reconstruct, or verification
	// on the receiving end (and our own round-trip test) fails on a spurious trailing CRLF.
	signedContent := bytes.TrimSuffix(entity, []byte("\r\n"))
	sig, err := Sign(certPEM, keyPEM, signedContent)
	if err != nil {
		return nil, err
	}
	boundary := fmt.Sprintf("smime_sig_%x", time.Now().UnixNano())

	var out bytes.Buffer
	for _, l := range topLines {
		out.WriteString(l + "\r\n")
	}
	fmt.Fprintf(&out, "Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"%s\"\r\n\r\n", boundary)
	out.WriteString("--" + boundary + "\r\n")
	out.Write(signedContent)
	out.WriteString("\r\n--" + boundary + "\r\n")
	out.WriteString("Content-Type: application/pkcs7-signature; name=\"smime.p7s\"\r\n")
	out.WriteString("Content-Transfer-Encoding: base64\r\n")
	out.WriteString("Content-Disposition: attachment; filename=\"smime.p7s\"\r\n\r\n")
	out.WriteString(base64Wrap(sig))
	out.WriteString("\r\n--" + boundary + "--\r\n")
	return out.Bytes(), nil
}

// entityHeaderNames are the headers that describe a MIME entity's own content (as opposed
// to the surrounding message envelope) and so must travel INSIDE the signed/encrypted part,
// not stay behind as a stray top-level header of the wrapper message.
var entityHeaderNames = []string{"Content-Type", "Content-Transfer-Encoding", "Content-Disposition"}

// splitMIMEEntity splits a raw RFC 5322 message into: the top-level headers with the
// entity headers removed (as lines, unfolded continuation joined), and the "entity" being
// protected — its own Content-Type/Content-Transfer-Encoding/Content-Disposition headers
// plus blank line plus body — which is what gets signed/encrypted, per RFC 1847.
func splitMIMEEntity(raw []byte) (topLines []string, entity []byte, err error) {
	idx := bytes.Index(raw, []byte("\r\n\r\n"))
	if idx < 0 {
		return nil, nil, errors.New("no header/body separator found in message")
	}
	headerBlock := string(raw[:idx])
	body := raw[idx+4:]
	rest := strings.Split(headerBlock, "\r\n")

	var entityLines []string
	for _, name := range entityHeaderNames {
		var val string
		val, rest = extractHeader(rest, name)
		if val != "" {
			entityLines = append(entityLines, val)
		}
	}
	if len(entityLines) == 0 {
		return nil, nil, errors.New("no Content-Type header found in message")
	}
	entity = append([]byte(strings.Join(entityLines, "\r\n")+"\r\n\r\n"), body...)
	return rest, entity, nil
}

// extractHeader pulls the named header (plus any folded continuation lines) out of lines,
// returning its full value and the remaining lines with it removed.
func extractHeader(lines []string, name string) (value string, rest []string) {
	prefix := strings.ToLower(name) + ":"
	for i, l := range lines {
		if strings.HasPrefix(strings.ToLower(l), prefix) {
			value = l
			j := i + 1
			for j < len(lines) && (strings.HasPrefix(lines[j], " ") || strings.HasPrefix(lines[j], "\t")) {
				value += "\r\n" + lines[j]
				j++
			}
			rest = append(append([]string{}, lines[:i]...), lines[j:]...)
			return value, rest
		}
	}
	return "", lines
}

// base64Wrap base64-encodes data and wraps it at 76 chars per line (RFC 2045).
func base64Wrap(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	var out strings.Builder
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		out.WriteString(encoded[i:end])
		if end < len(encoded) {
			out.WriteString("\r\n")
		}
	}
	return out.String()
}
