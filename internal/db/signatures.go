package db

import (
	"database/sql"

	"github.com/ghostersk/gowebmail/internal/models"
)

// ---- Signatures ----

// ListSignatures returns all signatures owned by a user.
func (d *DB) ListSignatures(userID int64) ([]models.Signature, error) {
	rows, err := d.sql.Query(
		`SELECT id, user_id, name, content_html, created_at FROM signatures WHERE user_id=? ORDER BY name`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Signature
	for rows.Next() {
		var s models.Signature
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.ContentHTML, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSignature fetches one signature scoped to its owning user.
func (d *DB) GetSignature(userID, id int64) (*models.Signature, error) {
	s := &models.Signature{}
	err := d.sql.QueryRow(
		`SELECT id, user_id, name, content_html, created_at FROM signatures WHERE user_id=? AND id=?`, userID, id,
	).Scan(&s.ID, &s.UserID, &s.Name, &s.ContentHTML, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

// CreateSignature inserts a new signature and returns its id.
func (d *DB) CreateSignature(userID int64, name, contentHTML string) (int64, error) {
	res, err := d.sql.Exec(
		`INSERT INTO signatures (user_id, name, content_html) VALUES (?,?,?)`, userID, name, contentHTML,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSignature updates name/content of a signature (scoped to owner).
func (d *DB) UpdateSignature(userID, id int64, name, contentHTML string) error {
	_, err := d.sql.Exec(
		`UPDATE signatures SET name=?, content_html=? WHERE id=? AND user_id=?`, name, contentHTML, id, userID,
	)
	return err
}

// DeleteSignature removes a signature (scoped to owner). Any signature_defaults rows
// pointing at it are cleared automatically via ON DELETE SET NULL.
func (d *DB) DeleteSignature(userID, id int64) error {
	_, err := d.sql.Exec(`DELETE FROM signatures WHERE id=? AND user_id=?`, id, userID)
	return err
}

// GetSignatureDefaults returns the default-new/default-reply signature ids for an account.
// Returns a zero-value struct (no error) if the account has no defaults row yet.
func (d *DB) GetSignatureDefaults(accountID int64) (models.SignatureDefaults, error) {
	sd := models.SignatureDefaults{AccountID: accountID}
	var newID, replyID sql.NullInt64
	err := d.sql.QueryRow(
		`SELECT default_new_id, default_reply_id FROM signature_defaults WHERE account_id=?`, accountID,
	).Scan(&newID, &replyID)
	if err == sql.ErrNoRows {
		return sd, nil
	}
	if err != nil {
		return sd, err
	}
	sd.DefaultNewID = newID.Int64
	sd.DefaultReplyID = replyID.Int64
	return sd, nil
}

// SetSignatureDefaults upserts which signature is default-for-new / default-for-reply on an account.
// A ProviderID of 0 clears that default (stored as NULL).
func (d *DB) SetSignatureDefaults(accountID, defaultNewID, defaultReplyID int64) error {
	var newVal, replyVal interface{}
	if defaultNewID > 0 {
		newVal = defaultNewID
	}
	if defaultReplyID > 0 {
		replyVal = defaultReplyID
	}
	_, err := d.sql.Exec(
		`INSERT INTO signature_defaults (account_id, default_new_id, default_reply_id) VALUES (?,?,?)
		 ON CONFLICT(account_id) DO UPDATE SET default_new_id=excluded.default_new_id, default_reply_id=excluded.default_reply_id`,
		accountID, newVal, replyVal,
	)
	return err
}
