// Package syncer provides background IMAP synchronisation for all active accounts.
package syncer

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/yourusername/gomail/internal/db"
	"github.com/yourusername/gomail/internal/email"
	"github.com/yourusername/gomail/internal/models"
)

// Scheduler runs background sync for all active accounts according to their
// individual sync_interval settings.
type Scheduler struct {
	db   *db.DB
	stop chan struct{}
}

// New creates a new Scheduler. Call Start() to begin background syncing.
func New(database *db.DB) *Scheduler {
	return &Scheduler{db: database, stop: make(chan struct{})}
}

// Start launches the scheduler goroutine. Ticks every minute and checks
// which accounts are due for sync based on last_sync and sync_interval.
func (s *Scheduler) Start() {
	go func() {
		log.Println("Background sync scheduler started")
		s.runDue()
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runDue()
			case <-s.stop:
				log.Println("Background sync scheduler stopped")
				return
			}
		}
	}()
}

// Stop signals the scheduler to exit.
func (s *Scheduler) Stop() {
	close(s.stop)
}

func (s *Scheduler) runDue() {
	accounts, err := s.db.ListAllActiveAccounts()
	if err != nil {
		log.Printf("Sync scheduler: list accounts: %v", err)
		return
	}
	now := time.Now()
	for _, account := range accounts {
		if account.SyncInterval <= 0 {
			continue
		}
		nextSync := account.LastSync.Add(time.Duration(account.SyncInterval) * time.Minute)
		if account.LastSync.IsZero() || now.After(nextSync) {
			go s.syncAccount(account)
		}
	}
}

// SyncAccountNow performs an immediate sync of one account. Returns messages synced.
func (s *Scheduler) SyncAccountNow(accountID int64) (int, error) {
	account, err := s.db.GetAccount(accountID)
	if err != nil || account == nil {
		return 0, fmt.Errorf("account %d not found", accountID)
	}
	return s.doSync(account)
}

// SyncFolderNow syncs a single folder for an account.
func (s *Scheduler) SyncFolderNow(accountID, folderID int64) (int, error) {
	account, err := s.db.GetAccount(accountID)
	if err != nil || account == nil {
		return 0, fmt.Errorf("account %d not found", accountID)
	}
	folder, err := s.db.GetFolderByID(folderID)
	if err != nil || folder == nil || folder.AccountID != accountID {
		return 0, fmt.Errorf("folder %d not found", folderID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := email.Connect(ctx, account)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	days := account.SyncDays
	if days <= 0 || account.SyncMode == "all" {
		days = 36500 // ~100 years = full mailbox
	}
	messages, err := c.FetchMessages(folder.FullPath, days)
	if err != nil {
		return 0, err
	}
	synced := 0
	for _, msg := range messages {
		msg.FolderID = folder.ID
		if err := s.db.UpsertMessage(msg); err == nil {
			synced++
		}
	}
	s.db.UpdateFolderCounts(folder.ID)
	s.db.UpdateAccountLastSync(accountID)
	return synced, nil
}

func (s *Scheduler) syncAccount(account *models.EmailAccount) {
	synced, err := s.doSync(account)
	if err != nil {
		log.Printf("Sync [%s]: %v", account.EmailAddress, err)
		s.db.SetAccountError(account.ID, err.Error())
		s.db.WriteAudit(nil, models.AuditAppError,
			"sync error for "+account.EmailAddress+": "+err.Error(), "", "")
		return
	}
	s.db.ClearAccountError(account.ID)
	if synced > 0 {
		log.Printf("Synced %d messages for %s", synced, account.EmailAddress)
	}
}

func (s *Scheduler) doSync(account *models.EmailAccount) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c, err := email.Connect(ctx, account)
	if err != nil {
		return 0, err
	}
	defer c.Close()

	mailboxes, err := c.ListMailboxes()
	if err != nil {
		return 0, fmt.Errorf("list mailboxes: %w", err)
	}

	synced := 0
	for _, mb := range mailboxes {
		folderType := email.InferFolderType(mb.Name, mb.Attributes)

		folder := &models.Folder{
			AccountID:  account.ID,
			Name:       mb.Name,
			FullPath:   mb.Name,
			FolderType: folderType,
		}
		if err := s.db.UpsertFolder(folder); err != nil {
			log.Printf("Upsert folder %s: %v", mb.Name, err)
			continue
		}

		dbFolder, _ := s.db.GetFolderByPath(account.ID, mb.Name)
		if dbFolder == nil {
			continue
		}

		// Skip folders that the user has disabled sync on
		if !dbFolder.SyncEnabled {
			continue
		}

		days := account.SyncDays
		if days <= 0 || account.SyncMode == "all" {
			days = 36500 // ~100 years = full mailbox
		}
		messages, err := c.FetchMessages(mb.Name, days)
		if err != nil {
			log.Printf("Fetch %s/%s: %v", account.EmailAddress, mb.Name, err)
			continue
		}

		for _, msg := range messages {
			msg.FolderID = dbFolder.ID
			if err := s.db.UpsertMessage(msg); err == nil {
				synced++
			}
		}

		s.db.UpdateFolderCounts(dbFolder.ID)
	}

	s.db.UpdateAccountLastSync(account.ID)
	return synced, nil
}
