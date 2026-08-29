package db

import (
	"database/sql"
	"time"

	"github.com/ghostersk/gowebmail/internal/models"
)

// ---- S/MIME identities ----
// key_pem is encrypted at rest via d.enc (internal/crypto.Encryptor), same as OAuth tokens elsewhere.

// ListSMIMEIdentities returns all S/MIME identities for an account (key_pem decrypted).
func (d *DB) ListSMIMEIdentities(accountID int64) ([]models.SMIMEIdentity, error) {
	rows, err := d.sql.Query(
		`SELECT id, account_id, cert_pem, key_pem, not_after, created_at FROM smime_identities WHERE account_id=? ORDER BY created_at DESC`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.SMIMEIdentity
	for rows.Next() {
		var s models.SMIMEIdentity
		var keyEnc string
		if err := rows.Scan(&s.ID, &s.AccountID, &s.CertPEM, &keyEnc, &s.NotAfter, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.KeyPEM, _ = d.enc.Decrypt(keyEnc)
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSMIMEIdentity fetches one S/MIME identity scoped to its account (key_pem decrypted).
func (d *DB) GetSMIMEIdentity(accountID, id int64) (*models.SMIMEIdentity, error) {
	s := &models.SMIMEIdentity{}
	var keyEnc string
	err := d.sql.QueryRow(
		`SELECT id, account_id, cert_pem, key_pem, not_after, created_at FROM smime_identities WHERE account_id=? AND id=?`,
		accountID, id,
	).Scan(&s.ID, &s.AccountID, &s.CertPEM, &keyEnc, &s.NotAfter, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.KeyPEM, _ = d.enc.Decrypt(keyEnc)
	return s, nil
}

// CreateSMIMEIdentity encrypts keyPEM at rest and inserts a new identity, returning its id.
func (d *DB) CreateSMIMEIdentity(accountID int64, certPEM, keyPEM string, notAfter time.Time) (int64, error) {
	keyEnc, err := d.enc.Encrypt(keyPEM)
	if err != nil {
		return 0, err
	}
	res, err := d.sql.Exec(
		`INSERT INTO smime_identities (account_id, cert_pem, key_pem, not_after) VALUES (?,?,?,?)`,
		accountID, certPEM, keyEnc, notAfter,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteSMIMEIdentity removes an identity (scoped to account_id).
func (d *DB) DeleteSMIMEIdentity(accountID, id int64) error {
	_, err := d.sql.Exec(`DELETE FROM smime_identities WHERE id=? AND account_id=?`, id, accountID)
	return err
}

// ---- S/MIME contact certs (per-user address book, unencrypted — public certs only) ----

func (d *DB) ListSMIMEContacts(userID int64) ([]models.SMIMEContact, error) {
	rows, err := d.sql.Query(
		`SELECT id, user_id, email, cert_pem, created_at FROM smime_contacts WHERE user_id=? ORDER BY email`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.SMIMEContact
	for rows.Next() {
		var c models.SMIMEContact
		if err := rows.Scan(&c.ID, &c.UserID, &c.Email, &c.CertPEM, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetSMIMEContactByEmail looks up a contact's cert by address (used when signer/encryptor
// needs to know if a recipient has a cert on file). Returns nil, nil if not found.
func (d *DB) GetSMIMEContactByEmail(userID int64, email string) (*models.SMIMEContact, error) {
	c := &models.SMIMEContact{}
	err := d.sql.QueryRow(
		`SELECT id, user_id, email, cert_pem, created_at FROM smime_contacts WHERE user_id=? AND email=? COLLATE NOCASE`,
		userID, email,
	).Scan(&c.ID, &c.UserID, &c.Email, &c.CertPEM, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// UpsertSMIMEContact adds or replaces a contact's cert for an email address.
func (d *DB) UpsertSMIMEContact(userID int64, email, certPEM string) error {
	_, err := d.sql.Exec(
		`INSERT INTO smime_contacts (user_id, email, cert_pem) VALUES (?,?,?)
		 ON CONFLICT(user_id, email) DO UPDATE SET cert_pem=excluded.cert_pem`,
		userID, email, certPEM,
	)
	return err
}

// DeleteSMIMEContact removes a contact cert (scoped to owner).
func (d *DB) DeleteSMIMEContact(userID, id int64) error {
	_, err := d.sql.Exec(`DELETE FROM smime_contacts WHERE id=? AND user_id=?`, id, userID)
	return err
}
