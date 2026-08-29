package pgp

import (
	"sync"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// Cache holds unlocked (passphrase-decrypted) PGP identities in memory, scoped to the
// session that unlocked them — never written to disk. No TTL: memory-bounded by active
// sessions, cleared only on explicit logout (see internal/handlers/auth.go Logout).
type Cache struct {
	mu    sync.Mutex
	byTok map[string]map[int64]*openpgp.Entity // sessionToken -> identityID -> unlocked entity
}

// NewCache creates an empty unlocked-key cache.
func NewCache() *Cache {
	return &Cache{byTok: make(map[string]map[int64]*openpgp.Entity)}
}

// Get returns the unlocked entity for identityID under sessionToken, if present.
func (c *Cache) Get(sessionToken string, identityID int64) (*openpgp.Entity, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.byTok[sessionToken]
	if !ok {
		return nil, false
	}
	e, ok := m[identityID]
	return e, ok
}

// Put stores an unlocked entity under sessionToken.
func (c *Cache) Put(sessionToken string, identityID int64, entity *openpgp.Entity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.byTok[sessionToken]
	if !ok {
		m = make(map[int64]*openpgp.Entity)
		c.byTok[sessionToken] = m
	}
	m[identityID] = entity
}

// ClearSession discards every unlocked identity for a session (call on logout).
func (c *Cache) ClearSession(sessionToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byTok, sessionToken)
}
