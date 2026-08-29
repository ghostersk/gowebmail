package db

import (
	"database/sql"

	"github.com/ghostersk/gowebmail/internal/models"
)

// ---- PGP identities ----
// private_key_armor relies on OpenPGP's own native S2K passphrase protection (no app-layer
// encryption needed here, unlike smime.go's key_pem) — stored exactly as produced.

func (d *DB) ListPGPIdentities(accountID int64) ([]models.PGPIdentity, error) {
	rows, err := d.sql.Query(
		`SELECT id, account_id, label, email, fingerprint, public_key_armor, private_key_armor, created_at
		 FROM pgp_identities WHERE account_id=? ORDER BY created_at DESC`, accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.PGPIdentity
	for rows.Next() {
		var p models.PGPIdentity
		if err := rows.Scan(&p.ID, &p.AccountID, &p.Label, &p.Email, &p.Fingerprint, &p.PublicKeyArmor, &p.PrivateKeyArmor, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) GetPGPIdentity(accountID, id int64) (*models.PGPIdentity, error) {
	p := &models.PGPIdentity{}
	err := d.sql.QueryRow(
		`SELECT id, account_id, label, email, fingerprint, public_key_armor, private_key_armor, created_at
		 FROM pgp_identities WHERE account_id=? AND id=?`, accountID, id,
	).Scan(&p.ID, &p.AccountID, &p.Label, &p.Email, &p.Fingerprint, &p.PublicKeyArmor, &p.PrivateKeyArmor, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (d *DB) CreatePGPIdentity(accountID int64, label, email, fingerprint, publicKeyArmor, privateKeyArmor string) (int64, error) {
	res, err := d.sql.Exec(
		`INSERT INTO pgp_identities (account_id, label, email, fingerprint, public_key_armor, private_key_armor) VALUES (?,?,?,?,?,?)`,
		accountID, label, email, fingerprint, publicKeyArmor, privateKeyArmor,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) DeletePGPIdentity(accountID, id int64) error {
	_, err := d.sql.Exec(`DELETE FROM pgp_identities WHERE id=? AND account_id=?`, id, accountID)
	return err
}

// ---- PGP contact public keys (per-user address book) ----

func (d *DB) ListPGPContacts(userID int64) ([]models.PGPContact, error) {
	rows, err := d.sql.Query(
		`SELECT id, user_id, email, label, fingerprint, public_key_armor, created_at FROM pgp_contacts WHERE user_id=? ORDER BY email`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.PGPContact
	for rows.Next() {
		var c models.PGPContact
		if err := rows.Scan(&c.ID, &c.UserID, &c.Email, &c.Label, &c.Fingerprint, &c.PublicKeyArmor, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetPGPContactByEmail looks up a contact's public key by address. Returns nil, nil if not found.
func (d *DB) GetPGPContactByEmail(userID int64, email string) (*models.PGPContact, error) {
	c := &models.PGPContact{}
	err := d.sql.QueryRow(
		`SELECT id, user_id, email, label, fingerprint, public_key_armor, created_at FROM pgp_contacts WHERE user_id=? AND email=? COLLATE NOCASE`,
		userID, email,
	).Scan(&c.ID, &c.UserID, &c.Email, &c.Label, &c.Fingerprint, &c.PublicKeyArmor, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func (d *DB) UpsertPGPContact(userID int64, email, label, fingerprint, publicKeyArmor string) error {
	_, err := d.sql.Exec(
		`INSERT INTO pgp_contacts (user_id, email, label, fingerprint, public_key_armor) VALUES (?,?,?,?,?)
		 ON CONFLICT(user_id, email) DO UPDATE SET label=excluded.label, fingerprint=excluded.fingerprint, public_key_armor=excluded.public_key_armor`,
		userID, email, label, fingerprint, publicKeyArmor,
	)
	return err
}

func (d *DB) DeletePGPContact(userID, id int64) error {
	_, err := d.sql.Exec(`DELETE FROM pgp_contacts WHERE id=? AND user_id=?`, id, userID)
	return err
}
