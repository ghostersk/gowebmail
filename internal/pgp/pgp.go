// Package pgp provides PGP key generation and RFC 3156 (PGP/MIME) encryption for
// outgoing mail, using github.com/ProtonMail/go-crypto — the maintained fork of
// golang.org/x/crypto/openpgp, which its own doc comment calls deprecated and
// "unsafe by design". This package is encryption-only: no PGP signature generation
// or verification (S/MIME, internal/smime, handles signing).
package pgp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

func defaultConfig() *packet.Config {
	return &packet.Config{
		DefaultCipher: packet.CipherAES256, // library default is AES-128
		RSABits:       2048,
	}
}

// GenerateKeyPair creates a new RSA-2048 keypair for email, protecting the private key
// with passphrase using OpenPGP's own native S2K format — no extra app-layer wrapping
// needed (unlike internal/smime's key_pem, which is encrypted at rest by the caller).
func GenerateKeyPair(email, passphrase string) (publicArmor, privateArmor []byte, err error) {
	config := defaultConfig()
	entity, err := openpgp.NewEntity(email, "", email, config)
	if err != nil {
		return nil, nil, fmt.Errorf("generate entity: %w", err)
	}
	if err := lockEntity(entity, passphrase); err != nil {
		return nil, nil, err
	}
	publicArmor, err = serializePublic(entity)
	if err != nil {
		return nil, nil, err
	}
	privateArmor, err = serializePrivate(entity, config)
	if err != nil {
		return nil, nil, err
	}
	return publicArmor, privateArmor, nil
}

func lockEntity(entity *openpgp.Entity, passphrase string) error {
	if err := entity.PrivateKey.Encrypt([]byte(passphrase)); err != nil {
		return fmt.Errorf("lock primary key: %w", err)
	}
	for _, sub := range entity.Subkeys {
		if sub.PrivateKey == nil {
			continue
		}
		if err := sub.PrivateKey.Encrypt([]byte(passphrase)); err != nil {
			return fmt.Errorf("lock subkey: %w", err)
		}
	}
	return nil
}

func serializePublic(entity *openpgp.Entity) ([]byte, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, err
	}
	if err := entity.Serialize(w); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func serializePrivate(entity *openpgp.Entity, config *packet.Config) ([]byte, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return nil, err
	}
	// Must use SerializePrivateWithoutSigning: SerializePrivate re-signs identities,
	// which requires the (now-encrypted) private key and fails once it's locked.
	if err := entity.SerializePrivateWithoutSigning(w, config); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ImportPrivateKey parses an armored private key (already passphrase-protected, e.g.
// exported from GnuPG) and re-serializes its public/private halves in our storage form.
func ImportPrivateKey(armoredData []byte, passphrase string) (publicArmor, privateArmor []byte, err error) {
	entity, err := ParsePrivateKey(armoredData)
	if err != nil {
		return nil, nil, err
	}
	// Verify the passphrase actually unlocks it before accepting the import.
	if err := UnlockPrivateKey(entity, passphrase); err != nil {
		return nil, nil, fmt.Errorf("passphrase does not unlock key: %w", err)
	}
	publicArmor, err = serializePublic(entity)
	if err != nil {
		return nil, nil, err
	}
	privateArmor = armoredData
	return publicArmor, privateArmor, nil
}

// ParsePublicKey reads a single armored public key.
func ParsePublicKey(armoredData []byte) (*openpgp.Entity, error) {
	return parseEntity(armoredData)
}

// ParsePrivateKey reads a single armored private key. The key remains locked
// (Encrypted) until UnlockPrivateKey is called with its passphrase.
func ParsePrivateKey(armoredData []byte) (*openpgp.Entity, error) {
	return parseEntity(armoredData)
}

func parseEntity(armoredData []byte) (*openpgp.Entity, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armoredData))
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	if len(entities) == 0 {
		return nil, fmt.Errorf("no key found in armored data")
	}
	return entities[0], nil
}

// UnlockPrivateKey decrypts the primary key and every subkey using passphrase.
func UnlockPrivateKey(entity *openpgp.Entity, passphrase string) error {
	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		if err := entity.PrivateKey.Decrypt([]byte(passphrase)); err != nil {
			return fmt.Errorf("unlock primary key: %w", err)
		}
	}
	for _, sub := range entity.Subkeys {
		if sub.PrivateKey != nil && sub.PrivateKey.Encrypted {
			if err := sub.PrivateKey.Decrypt([]byte(passphrase)); err != nil {
				return fmt.Errorf("unlock subkey: %w", err)
			}
		}
	}
	return nil
}

// Fingerprint returns the entity's primary key fingerprint as uppercase hex.
func Fingerprint(entity *openpgp.Entity) string {
	return strings.ToUpper(fmt.Sprintf("%x", entity.PrimaryKey.Fingerprint))
}

// EncryptEntity produces an RFC 3156 (PGP/MIME) armored encrypted message for the given
// recipients' public keys.
func EncryptEntity(raw []byte, recipients []*openpgp.Entity) ([]byte, error) {
	var buf bytes.Buffer
	aw, err := armor.Encode(&buf, "PGP MESSAGE", nil)
	if err != nil {
		return nil, err
	}
	pt, err := openpgp.Encrypt(aw, recipients, nil, nil, defaultConfig())
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	if _, err := pt.Write(raw); err != nil {
		return nil, err
	}
	if err := pt.Close(); err != nil {
		return nil, err
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecryptEntity opens an armored PGP message using an already-unlocked identity
// (see UnlockPrivateKey).
func DecryptEntity(armored []byte, unlockedIdentity *openpgp.Entity) ([]byte, error) {
	block, err := armor.Decode(bytes.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("decode armor: %w", err)
	}
	keyring := openpgp.EntityList{unlockedIdentity}
	md, err := openpgp.ReadMessage(block.Body, keyring, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}
	return io.ReadAll(md.UnverifiedBody)
}

// ---- Whole-message MIME wrapping (RFC 3156 multipart/encrypted) ----

// EncryptMIME wraps a complete raw MIME message (headers + body, as produced by
// internal/email's buildMIMEMessage) in an RFC 3156 multipart/encrypted structure: the
// original Content-Type + body are PGP-encrypted as one opaque unit for recipients, and
// all other top-level headers (From, To, Subject, Date, Message-ID, ...) are preserved.
// Unlike SignMIME's CMS wrapping, no CRLF/boundary canonicalization concern applies here —
// the encrypted blob is opaque to any downstream MIME parser, so decryption returns exactly
// what was encrypted regardless of a trailing CRLF.
func EncryptMIME(raw []byte, recipients []*openpgp.Entity) ([]byte, error) {
	topLines, entity, err := splitMIMEEntity(raw)
	if err != nil {
		return nil, err
	}
	encrypted, err := EncryptEntity(entity, recipients)
	if err != nil {
		return nil, err
	}
	boundary := fmt.Sprintf("pgp_enc_%x", time.Now().UnixNano())

	var out bytes.Buffer
	for _, l := range topLines {
		out.WriteString(l + "\r\n")
	}
	fmt.Fprintf(&out, "Content-Type: multipart/encrypted; protocol=\"application/pgp-encrypted\"; boundary=\"%s\"\r\n\r\n", boundary)
	out.WriteString("--" + boundary + "\r\n")
	out.WriteString("Content-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n")
	out.WriteString("--" + boundary + "\r\n")
	out.WriteString("Content-Type: application/octet-stream; name=\"encrypted.asc\"\r\n")
	out.WriteString("Content-Description: OpenPGP encrypted message\r\n")
	out.WriteString("Content-Disposition: inline; filename=\"encrypted.asc\"\r\n\r\n")
	out.Write(encrypted)
	out.WriteString("\r\n--" + boundary + "--\r\n")
	return out.Bytes(), nil
}

// entityHeaderNames are the headers that describe a MIME entity's own content (as opposed
// to the surrounding message envelope) and so must travel INSIDE the encrypted part, not
// stay behind as a stray top-level header of the wrapper message.
var entityHeaderNames = []string{"Content-Type", "Content-Transfer-Encoding", "Content-Disposition"}

// splitMIMEEntity splits a raw RFC 5322 message into the top-level headers with the entity
// headers removed, and the "entity" being protected — its own Content-Type/Content-Transfer-
// Encoding/Content-Disposition headers plus blank line plus body.
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
