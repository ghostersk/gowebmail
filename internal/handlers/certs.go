package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"

	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/middleware"
	"github.com/ghostersk/gowebmail/internal/models"
	"github.com/ghostersk/gowebmail/internal/pgp"
	"github.com/ghostersk/gowebmail/internal/smime"
)

// dbSigner builds a signed/encrypted outgoing message from whatever S/MIME identity and
// PGP contact keys the sending account/user actually has on file. Implements
// internal/email.Signer. Sign first (if an S/MIME identity exists for the account), then
// encrypt (if every recipient has a PGP contact key on file) — matches the reference
// design: "S/MIME certificates sign... PGP keys encrypt...".
type dbSigner struct {
	db     *db.DB
	userID int64
}

func (s *dbSigner) SignAndEncrypt(account *models.EmailAccount, recipients []string, raw []byte) ([]byte, error) {
	out := raw

	identities, err := s.db.ListSMIMEIdentities(account.ID)
	if err == nil && len(identities) > 0 {
		id := identities[0]
		signed, err := smime.SignMIME([]byte(id.CertPEM), []byte(id.KeyPEM), out)
		if err != nil {
			return nil, fmt.Errorf("smime sign: %w", err)
		}
		out = signed
	}

	if len(recipients) > 0 {
		var pgpEntities []*openpgp.Entity
		allHaveKeys := true
		for _, addr := range recipients {
			contact, err := s.db.GetPGPContactByEmail(s.userID, addr)
			if err != nil || contact == nil {
				allHaveKeys = false
				break
			}
			entity, err := pgp.ParsePublicKey([]byte(contact.PublicKeyArmor))
			if err != nil {
				allHaveKeys = false
				break
			}
			pgpEntities = append(pgpEntities, entity)
		}
		if allHaveKeys && len(pgpEntities) > 0 {
			encrypted, err := pgp.EncryptMIME(out, pgpEntities)
			if err != nil {
				return nil, fmt.Errorf("pgp encrypt: %w", err)
			}
			out = encrypted
		}
	}

	return out, nil
}

// newSigner builds a Signer for outgoing mail on this account/user, or nil if no S/MIME
// identity and no PGP recipient keys apply — SendMessageFull treats nil as a no-op.
func (h *APIHandler) newSigner(userID int64) *dbSigner {
	return &dbSigner{db: h.db, userID: userID}
}

// ---- S/MIME handlers ----

func (h *APIHandler) SMIMEIdentity(w http.ResponseWriter, r *http.Request) {
	accountID := queryInt64(r, "account_id", 0)
	if accountID == 0 || !h.ownAccount(w, r, accountID) {
		if accountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	identities, err := h.db.ListSMIMEIdentities(accountID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list identities")
		return
	}
	h.writeJSON(w, identities)
}

func (h *APIHandler) SMIMEGenerate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID int64 `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AccountID == 0 {
		h.writeError(w, http.StatusBadRequest, "account_id required")
		return
	}
	if !h.ownAccount(w, r, req.AccountID) {
		return
	}
	account, _ := h.db.GetAccount(req.AccountID)
	certPEM, keyPEM, err := smime.GenerateSelfSigned(account.EmailAddress, smime.DefaultValidity)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to generate certificate")
		return
	}
	cert, _ := smime.ParseCertPEM(certPEM)
	id, err := h.db.CreateSMIMEIdentity(req.AccountID, string(certPEM), string(keyPEM), cert.NotAfter)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to store identity")
		return
	}
	h.writeJSON(w, map[string]interface{}{"id": id, "ok": true})
}

func (h *APIHandler) SMIMEImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid form")
		return
	}
	accountID := queryInt64(r, "account_id", 0)
	if a, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64); a > 0 {
		accountID = a
	}
	if accountID == 0 || !h.ownAccount(w, r, accountID) {
		if accountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	file, _, err := r.FormFile("p12_file")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "p12_file required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "failed to read file")
		return
	}
	password := r.FormValue("p12_password")
	certPEM, keyPEM, err := smime.ImportPKCS12(data, password)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "failed to import: "+err.Error())
		return
	}
	cert, _ := smime.ParseCertPEM(certPEM)
	notAfter := time.Now().Add(smime.DefaultValidity)
	if cert != nil {
		notAfter = cert.NotAfter
	}
	id, err := h.db.CreateSMIMEIdentity(accountID, string(certPEM), string(keyPEM), notAfter)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to store identity")
		return
	}
	h.writeJSON(w, map[string]interface{}{"id": id, "ok": true})
}

func (h *APIHandler) SMIMERemoveIdentity(w http.ResponseWriter, r *http.Request) {
	id := pathInt64(r, "id")
	accountID := queryInt64(r, "account_id", 0)
	if accountID == 0 || !h.ownAccount(w, r, accountID) {
		if accountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	if err := h.db.DeleteSMIMEIdentity(accountID, id); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete identity")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}

func (h *APIHandler) SMIMEContacts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	contacts, err := h.db.ListSMIMEContacts(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list contacts")
		return
	}
	h.writeJSON(w, contacts)
}

func (h *APIHandler) SMIMEAddContact(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid form")
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if email == "" {
		h.writeError(w, http.StatusBadRequest, "email required")
		return
	}
	file, _, err := r.FormFile("cert_file")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "cert_file required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "failed to read file")
		return
	}
	if _, err := smime.ParseCertPEM(data); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid certificate: "+err.Error())
		return
	}
	if err := h.db.UpsertSMIMEContact(userID, email, string(data)); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to save contact")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}

func (h *APIHandler) SMIMERemoveContact(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	if err := h.db.DeleteSMIMEContact(userID, id); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete contact")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}

// ---- PGP handlers ----

func (h *APIHandler) PGPIdentity(w http.ResponseWriter, r *http.Request) {
	accountID := queryInt64(r, "account_id", 0)
	if accountID == 0 || !h.ownAccount(w, r, accountID) {
		if accountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	identities, err := h.db.ListPGPIdentities(accountID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list identities")
		return
	}
	h.writeJSON(w, identities)
}

func (h *APIHandler) PGPGenerate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID  int64  `json:"account_id"`
		Label      string `json:"label"`
		Passphrase string `json:"passphrase"`
		Confirm    string `json:"passphrase_confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AccountID == 0 {
		h.writeError(w, http.StatusBadRequest, "account_id required")
		return
	}
	if !h.ownAccount(w, r, req.AccountID) {
		return
	}
	if len(req.Passphrase) < 8 {
		h.writeError(w, http.StatusBadRequest, "passphrase must be at least 8 characters")
		return
	}
	if req.Passphrase != req.Confirm {
		h.writeError(w, http.StatusBadRequest, "passphrases do not match")
		return
	}
	account, _ := h.db.GetAccount(req.AccountID)
	pubArmor, privArmor, err := pgp.GenerateKeyPair(account.EmailAddress, req.Passphrase)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to generate key")
		return
	}
	entity, _ := pgp.ParsePublicKey(pubArmor)
	fingerprint := ""
	if entity != nil {
		fingerprint = pgp.Fingerprint(entity)
	}
	id, err := h.db.CreatePGPIdentity(req.AccountID, req.Label, account.EmailAddress, fingerprint, string(pubArmor), string(privArmor))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to store identity")
		return
	}
	h.writeJSON(w, map[string]interface{}{"id": id, "ok": true})
}

func (h *APIHandler) PGPImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid form")
		return
	}
	accountID := queryInt64(r, "account_id", 0)
	if a, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64); a > 0 {
		accountID = a
	}
	if accountID == 0 || !h.ownAccount(w, r, accountID) {
		if accountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	passphrase := r.FormValue("passphrase")
	label := r.FormValue("label")
	file, _, err := r.FormFile("key_file")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "key_file required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "failed to read file")
		return
	}
	pubArmor, privArmor, err := pgp.ImportPrivateKey(data, passphrase)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "failed to import: "+err.Error())
		return
	}
	entity, _ := pgp.ParsePublicKey(pubArmor)
	email, fingerprint := "", ""
	if entity != nil {
		fingerprint = pgp.Fingerprint(entity)
		for name := range entity.Identities {
			if id := entity.Identities[name]; id.UserId != nil && id.UserId.Email != "" {
				email = id.UserId.Email
				break
			}
		}
	}
	account, _ := h.db.GetAccount(accountID)
	if email == "" && account != nil {
		email = account.EmailAddress
	}
	id, err := h.db.CreatePGPIdentity(accountID, label, email, fingerprint, string(pubArmor), string(privArmor))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to store identity")
		return
	}
	h.writeJSON(w, map[string]interface{}{"id": id, "ok": true})
}

func (h *APIHandler) PGPRemoveIdentity(w http.ResponseWriter, r *http.Request) {
	id := pathInt64(r, "id")
	accountID := queryInt64(r, "account_id", 0)
	if accountID == 0 || !h.ownAccount(w, r, accountID) {
		if accountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	if err := h.db.DeletePGPIdentity(accountID, id); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete identity")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}

// PGPUnlock verifies a passphrase decrypts the identity's private key, then caches the
// unlocked entity for this session (see internal/pgp.Cache) so a future decrypt-on-read
// of incoming PGP mail — not yet implemented — won't need to re-prompt for it. Cleared on
// logout (AuthHandler.Logout).
func (h *APIHandler) PGPUnlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IdentityID int64  `json:"identity_id"`
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IdentityID == 0 {
		h.writeError(w, http.StatusBadRequest, "identity_id required")
		return
	}
	accountID := queryInt64(r, "account_id", 0)
	if accountID == 0 || !h.ownAccount(w, r, accountID) {
		if accountID == 0 {
			h.writeError(w, http.StatusBadRequest, "account_id required")
		}
		return
	}
	identity, err := h.db.GetPGPIdentity(accountID, req.IdentityID)
	if err != nil || identity == nil {
		h.writeError(w, http.StatusNotFound, "identity not found")
		return
	}
	entity, err := pgp.ParsePrivateKey([]byte(identity.PrivateKeyArmor))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to parse key")
		return
	}
	if err := pgp.UnlockPrivateKey(entity, req.Passphrase); err != nil {
		h.writeError(w, http.StatusBadRequest, "incorrect passphrase")
		return
	}
	if h.pgpCache != nil {
		if cookie, err := r.Cookie("gomail_session"); err == nil {
			h.pgpCache.Put(cookie.Value, req.IdentityID, entity)
		}
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}

func (h *APIHandler) PGPContacts(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	contacts, err := h.db.ListPGPContacts(userID)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to list contacts")
		return
	}
	h.writeJSON(w, contacts)
}

func (h *APIHandler) PGPAddContact(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid form")
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if email == "" {
		h.writeError(w, http.StatusBadRequest, "email required")
		return
	}
	label := r.FormValue("label")
	file, _, err := r.FormFile("key_file")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "key_file required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "failed to read file")
		return
	}
	entity, err := pgp.ParsePublicKey(data)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid public key: "+err.Error())
		return
	}
	if err := h.db.UpsertPGPContact(userID, email, label, pgp.Fingerprint(entity), string(data)); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to save contact")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}

func (h *APIHandler) PGPRemoveContact(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	id := pathInt64(r, "id")
	if err := h.db.DeletePGPContact(userID, id); err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to delete contact")
		return
	}
	h.writeJSON(w, map[string]interface{}{"ok": true})
}
