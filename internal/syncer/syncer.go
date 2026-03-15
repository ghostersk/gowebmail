// Package syncer provides background IMAP synchronisation for all active accounts.
// Architecture:
//   - One goroutine per account runs IDLE on the INBOX to receive push notifications.
//   - A separate drain goroutine flushes pending_imap_ops (delete/move/flag writes).
//   - Periodic full-folder delta sync catches changes made by other clients.
package syncer

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ghostersk/gowebmail/internal/logger"
	"github.com/ghostersk/gowebmail/config"
	"github.com/ghostersk/gowebmail/internal/auth"
	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/email"
	"github.com/ghostersk/gowebmail/internal/graph"
	"github.com/ghostersk/gowebmail/internal/models"
)

// Scheduler coordinates all background sync activity.
type Scheduler struct {
	db   *db.DB
	cfg  *config.Config
	stop chan struct{}
	wg   sync.WaitGroup

	// push channels: accountID -> channel to signal "something changed on server"
	pushMu sync.Mutex
	pushCh map[int64]chan struct{}

	// reconcileCh signals the main loop to immediately check for new/removed accounts.
	reconcileCh chan struct{}
}

// New creates a new Scheduler.
func New(database *db.DB, cfg *config.Config) *Scheduler {
	return &Scheduler{
		db:          database,
		cfg:         cfg,
		stop:        make(chan struct{}),
		pushCh:      make(map[int64]chan struct{}),
		reconcileCh: make(chan struct{}, 1),
	}
}

// TriggerReconcile asks the main loop to immediately check for new accounts.
// Safe to call from any goroutine; non-blocking.
func (s *Scheduler) TriggerReconcile() {
	select {
	case s.reconcileCh <- struct{}{}:
	default:
	}
}

// Start launches all background goroutines.
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.mainLoop()
	}()
	log.Println("[sync] scheduler started")
}

// Stop signals all goroutines to exit and waits for them.
func (s *Scheduler) Stop() {
	close(s.stop)
	s.wg.Wait()
	log.Println("[sync] scheduler stopped")
}

// TriggerAccountSync signals an immediate sync for an account (called after IMAP write ops).
func (s *Scheduler) TriggerAccountSync(accountID int64) {
	s.pushMu.Lock()
	ch, ok := s.pushCh[accountID]
	s.pushMu.Unlock()
	if ok {
		select {
		case ch <- struct{}{}:
		default: // already pending
		}
	}
}

// ---- Main coordination loop ----

func (s *Scheduler) mainLoop() {
	// Ticker for the outer "check which accounts are due" loop.
	// Runs every 30s; individual accounts control their own interval.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Track per-account goroutines so we only launch one per account.
	type accountWorker struct {
		stop   chan struct{}
		pushCh chan struct{}
	}
	workers := make(map[int64]*accountWorker)

	spawnWorker := func(account *models.EmailAccount) {
		if _, exists := workers[account.ID]; exists {
			return
		}
		w := &accountWorker{
			stop:   make(chan struct{}),
			pushCh: make(chan struct{}, 1),
		}
		workers[account.ID] = w

		s.pushMu.Lock()
		s.pushCh[account.ID] = w.pushCh
		s.pushMu.Unlock()

		s.wg.Add(1)
		go func(acc *models.EmailAccount, w *accountWorker) {
			defer s.wg.Done()
			s.accountWorker(acc, w.stop, w.pushCh)
		}(account, w)
	}

	stopWorker := func(accountID int64) {
		if w, ok := workers[accountID]; ok {
			close(w.stop)
			delete(workers, accountID)
			s.pushMu.Lock()
			delete(s.pushCh, accountID)
			s.pushMu.Unlock()
		}
	}

	// Initial spawn
	s.spawnForActive(spawnWorker)

	for {
		select {
		case <-s.stop:
			for id := range workers {
				stopWorker(id)
			}
			return
		case <-s.reconcileCh:
			// Immediately check for new/removed accounts (e.g. after OAuth connect)
			activeIDs := make(map[int64]bool, len(workers))
			for id := range workers {
				activeIDs[id] = true
			}
			s.reconcileWorkers(activeIDs, spawnWorker, stopWorker)
		case <-ticker.C:
			// Build active IDs map for reconciliation
			activeIDs := make(map[int64]bool, len(workers))
			for id := range workers {
				activeIDs[id] = true
			}
			s.reconcileWorkers(activeIDs, spawnWorker, stopWorker)
		}
	}
}

func (s *Scheduler) spawnForActive(spawn func(*models.EmailAccount)) {
	accounts, err := s.db.ListAllActiveAccounts()
	if err != nil {
		log.Printf("[sync] list accounts: %v", err)
		return
	}
	for _, acc := range accounts {
		spawn(acc)
	}
}

func (s *Scheduler) reconcileWorkers(
	activeIDs map[int64]bool,
	spawn func(*models.EmailAccount),
	stop func(int64),
) {
	accounts, err := s.db.ListAllActiveAccounts()
	if err != nil {
		return
	}
	serverActive := make(map[int64]bool)
	for _, acc := range accounts {
		serverActive[acc.ID] = true
		if !activeIDs[acc.ID] {
			spawn(acc)
		}
	}
	for id := range activeIDs {
		if !serverActive[id] {
			stop(id)
		}
	}
}

// ---- Per-account worker ----
// Each worker:
//  1. On startup: drain pending ops, then do a full delta sync.
//  2. Runs an IDLE loop on INBOX for push notifications.
//  3. Every syncInterval minutes (or on push signal): delta sync all enabled folders.
//  4. Every 2 minutes: drain pending ops (retries failed writes).

func (s *Scheduler) accountWorker(account *models.EmailAccount, stop chan struct{}, push chan struct{}) {
	log.Printf("[sync] worker started for %s", account.EmailAddress)

	// Fresh account data function (interval can change at runtime)
	getAccount := func() *models.EmailAccount {
		a, _ := s.db.GetAccount(account.ID)
		if a == nil {
			return account
		}
		return a
	}

	// Graph-based accounts (personal outlook.com) use a different sync path
	if account.Provider == models.ProviderOutlookPersonal {
		s.graphWorker(account, stop, push)
		return
	}

	// Initial sync on startup
	s.drainPendingOps(account)
	s.deltaSync(getAccount())

	// Drain ticker: retry pending ops every 90 seconds
	drainTicker := time.NewTicker(90 * time.Second)
	defer drainTicker.Stop()

	// Full sync ticker: based on account sync_interval, check every 30s
	syncTicker := time.NewTicker(30 * time.Second)
	defer syncTicker.Stop()

	// IDLE watcher for INBOX push notifications
	idleCh := make(chan struct{}, 1)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.idleWatcher(account, stop, idleCh)
	}()

	for {
		select {
		case <-stop:
			log.Printf("[sync] worker stopped for %s", account.EmailAddress)
			return
		case <-drainTicker.C:
			s.drainPendingOps(getAccount())
		case <-idleCh:
			// Server signalled new mail/changes in INBOX — sync just INBOX
			acc := getAccount()
			s.syncInbox(acc)
		case <-push:
			// Local trigger (after write op) — drain ops then sync
			acc := getAccount()
			s.drainPendingOps(acc)
			s.deltaSync(acc)
		case <-syncTicker.C:
			acc := getAccount()
			if acc.SyncInterval <= 0 {
				continue
			}
			nextSync := acc.LastSync.Add(time.Duration(acc.SyncInterval) * time.Minute)
			if acc.LastSync.IsZero() || time.Now().After(nextSync) {
				s.deltaSync(acc)
			}
		}
	}
}

// ---- IDLE watcher ----
// Maintains a persistent IMAP connection to INBOX and issues IDLE.
// When EXISTS or EXPUNGE arrives, sends to idleCh.
func (s *Scheduler) idleWatcher(account *models.EmailAccount, stop chan struct{}, idleCh chan struct{}) {
	const reconnectDelay = 30 * time.Second
	const idleTimeout = 25 * time.Minute // RFC 2177 recommends < 29min

	signal := func() {
		select {
		case idleCh <- struct{}{}:
		default:
		}
	}

	for {
		select {
		case <-stop:
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		account = s.ensureFreshToken(account)
		c, err := email.Connect(ctx, account)
		cancel()
		if err != nil {
			log.Printf("[idle:%s] connect: %v — retry in %s", account.EmailAddress, err, reconnectDelay)
			select {
			case <-stop:
				return
			case <-time.After(reconnectDelay):
				continue
			}
		}

		// Select INBOX
		_, err = c.SelectMailbox("INBOX")
		if err != nil {
			c.Close()
			select {
			case <-stop:
				return
			case <-time.After(reconnectDelay):
				continue
			}
		}

		// IDLE loop — go-imap v1 does not have built-in IDLE, we poll with short
		// CHECK + NOOP and rely on the EXISTS response to wake us.
		// We use a 1-minute poll since go-imap v1 doesn't expose IDLE directly.
		pollTicker := time.NewTicker(60 * time.Second)
		idleTimer := time.NewTimer(idleTimeout)

	pollLoop:
		for {
			select {
			case <-stop:
				pollTicker.Stop()
				idleTimer.Stop()
				c.Close()
				return
			case <-idleTimer.C:
				// Reconnect to keep connection alive
				pollTicker.Stop()
				c.Close()
				break pollLoop
			case <-pollTicker.C:
				// Poll server for changes
				status, err := c.GetFolderStatus("INBOX")
				if err != nil {
					log.Printf("[idle:%s] status check: %v", account.EmailAddress, err)
					pollTicker.Stop()
					idleTimer.Stop()
					c.Close()
					break pollLoop
				}
				// Check if message count changed
				localCount := s.db.GetFolderMessageCount(account.ID, "INBOX")
				if status.Messages != uint32(localCount) {
					signal()
				}
			}
		}

		select {
		case <-stop:
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// ---- Delta sync ----
// For each enabled folder:
//   1. Check UIDVALIDITY — if changed, full re-sync (folder was recreated on server).
//   2. Fetch only new messages (UID > last_seen_uid).
//   3. Fetch FLAGS for all existing messages to catch read/star changes from other clients.
//   4. Fetch all server UIDs and purge locally deleted messages.

func (s *Scheduler) deltaSync(account *models.EmailAccount) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	account = s.ensureFreshToken(account)
	c, err := email.Connect(ctx, account)
	if err != nil {
		log.Printf("[sync:%s] connect: %v", account.EmailAddress, err)
		s.db.SetAccountError(account.ID, err.Error())
		return
	}
	defer c.Close()
	s.db.ClearAccountError(account.ID)

	mailboxes, err := c.ListMailboxes()
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "not connected") {
			// For personal outlook.com accounts: Microsoft does not issue JWT Bearer tokens
			// to custom Azure app registrations for IMAP OAuth — only opaque v1 tokens which
			// authenticate but cannot access the mailbox. This is a Microsoft platform limitation.
			// Workaround: use a Microsoft 365 work/school account, or add this account as a
			// standard IMAP account using an App Password from account.microsoft.com/security.
			errMsg = "IMAP OAuth is not supported for personal outlook.com accounts with custom Azure app registrations. " +
				"To connect this account: go to account.microsoft.com/security → Advanced security options → App passwords, " +
				"create an app password, then remove this account and re-add it as a standard IMAP account using " +
				"server: outlook.office365.com, port: 993, with your email and the app password."
		}
		log.Printf("[sync:%s] list mailboxes: %v", account.EmailAddress, err)
		s.db.SetAccountError(account.ID, errMsg)
		return
	}

	totalNew := 0
	for _, mb := range mailboxes {
		folderType := email.InferFolderType(mb.Name, mb.Attributes)
		folder := &models.Folder{
			AccountID:  account.ID,
			Name:       mb.Name,
			FullPath:   mb.Name,
			FolderType: folderType,
		}
		if err := s.db.UpsertFolder(folder); err != nil {
			continue
		}
		dbFolder, _ := s.db.GetFolderByPath(account.ID, mb.Name)
		if dbFolder == nil || !dbFolder.SyncEnabled {
			continue
		}

		n, err := s.syncFolder(c, account, dbFolder)
		if err != nil {
			log.Printf("[sync:%s] folder %s: %v", account.EmailAddress, mb.Name, err)
			continue
		}
		totalNew += n
	}

	s.db.UpdateAccountLastSync(account.ID)
	if totalNew > 0 {
		logger.Debug("[sync:%s] %d new messages", account.EmailAddress, totalNew)
	}
}

// syncInbox is a fast path that only syncs the INBOX folder.
func (s *Scheduler) syncInbox(account *models.EmailAccount) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	account = s.ensureFreshToken(account)
	c, err := email.Connect(ctx, account)
	if err != nil {
		return
	}
	defer c.Close()

	dbFolder, _ := s.db.GetFolderByPath(account.ID, "INBOX")
	if dbFolder == nil {
		return
	}
	n, err := s.syncFolder(c, account, dbFolder)
	if err != nil {
		log.Printf("[idle:%s] INBOX sync: %v", account.EmailAddress, err)
		return
	}
	if n > 0 {
		logger.Debug("[idle:%s] %d new messages in INBOX", account.EmailAddress, n)
	}
}

func (s *Scheduler) syncFolder(c *email.Client, account *models.EmailAccount, dbFolder *models.Folder) (int, error) {
	status, err := c.GetFolderStatus(dbFolder.FullPath)
	if err != nil {
		return 0, fmt.Errorf("status: %w", err)
	}

	storedValidity, lastSeenUID := s.db.GetFolderSyncState(dbFolder.ID)
	newMessages := 0

	// UIDVALIDITY changed = folder was recreated on server; wipe local and re-fetch all
	if storedValidity != 0 && status.UIDValidity != storedValidity {
		log.Printf("[sync] UIDVALIDITY changed for %s/%s — full re-sync", account.EmailAddress, dbFolder.FullPath)
		s.db.DeleteAllFolderMessages(dbFolder.ID)
		lastSeenUID = 0
	}

	// 1. Fetch new messages (UID > lastSeenUID)
	var msgs []*models.Message
	if lastSeenUID == 0 {
		// First sync: respect the account's days/all setting
		days := account.SyncDays
		if days <= 0 || account.SyncMode == "all" {
			days = 0
		}
		msgs, err = c.FetchMessages(dbFolder.FullPath, days)
	} else {
		msgs, err = c.FetchNewMessages(dbFolder.FullPath, lastSeenUID)
	}
	if err != nil {
		return 0, fmt.Errorf("fetch new: %w", err)
	}

	maxUID := lastSeenUID
	for _, msg := range msgs {
		msg.FolderID = dbFolder.ID
		if err := s.db.UpsertMessage(msg); err == nil {
			newMessages++
			// Save attachment metadata if any (enables download)
			if len(msg.Attachments) > 0 && msg.ID > 0 {
				_ = s.db.SaveAttachmentMeta(msg.ID, msg.Attachments)
			}
		}
		uid := uint32(0)
		fmt.Sscanf(msg.RemoteUID, "%d", &uid)
		if uid > maxUID {
			maxUID = uid
		}
	}

	// 2. Sync flags for ALL existing messages (catch read/star changes from other clients)
	flags, err := c.SyncFlags(dbFolder.FullPath)
	if err != nil {
		log.Printf("[sync] flags %s/%s: %v", account.EmailAddress, dbFolder.FullPath, err)
	} else if len(flags) > 0 {
		s.db.ReconcileFlags(dbFolder.ID, flags)
	}

	// 3. Fetch all server UIDs and purge messages deleted on server
	serverUIDs, err := c.ListAllUIDs(dbFolder.FullPath)
	if err != nil {
		log.Printf("[sync] list uids %s/%s: %v", account.EmailAddress, dbFolder.FullPath, err)
	} else {
		purged, _ := s.db.PurgeDeletedMessages(dbFolder.ID, serverUIDs)
		if purged > 0 {
			log.Printf("[sync] purged %d server-deleted messages from %s/%s", purged, account.EmailAddress, dbFolder.FullPath)
		}
	}

	// Save sync state
	s.db.SetFolderSyncState(dbFolder.ID, status.UIDValidity, maxUID)
	s.db.UpdateFolderCounts(dbFolder.ID)

	return newMessages, nil
}

// ---- Pending ops drain ----
// Applies queued IMAP write operations (delete/move/flag) with retry logic.

func (s *Scheduler) drainPendingOps(account *models.EmailAccount) {
	// Graph accounts don't use the IMAP ops queue
	if account.Provider == models.ProviderOutlookPersonal {
		return
	}
	ops, err := s.db.DequeuePendingOps(account.ID, 50)
	if err != nil || len(ops) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	account = s.ensureFreshToken(account)
	c, err := email.Connect(ctx, account)
	if err != nil {
		log.Printf("[ops:%s] connect for drain: %v", account.EmailAddress, err)
		return
	}
	defer c.Close()

	// Find trash folder name once
	trashName := ""
	if mboxes, err := c.ListMailboxes(); err == nil {
		for _, mb := range mboxes {
			if email.InferFolderType(mb.Name, mb.Attributes) == "trash" {
				trashName = mb.Name
				break
			}
		}
	}

	for _, op := range ops {
		var applyErr error
		switch op.OpType {
		case "delete":
			applyErr = c.DeleteByUID(op.FolderPath, op.RemoteUID, trashName)
		case "move":
			applyErr = c.MoveByUID(op.FolderPath, op.Extra, op.RemoteUID)
		case "flag_read":
			applyErr = c.SetFlagByUID(op.FolderPath, op.RemoteUID, `\Seen`, op.Extra == "1")
		case "flag_star":
			applyErr = c.SetFlagByUID(op.FolderPath, op.RemoteUID, `\Flagged`, op.Extra == "1")
		}

		if applyErr != nil {
			log.Printf("[ops:%s] %s uid=%d folder=%s: %v", account.EmailAddress, op.OpType, op.RemoteUID, op.FolderPath, applyErr)
			s.db.IncrementPendingOpAttempts(op.ID)
		} else {
			s.db.DeletePendingOp(op.ID)
		}
	}

	if n := s.db.CountPendingOps(account.ID); n > 0 {
		log.Printf("[ops:%s] %d ops still pending after drain", account.EmailAddress, n)
	}
}

// ---- OAuth token refresh ----

// ensureFreshToken checks whether an OAuth account's access token is near
// expiry and, if so, exchanges the refresh token for a new one, persists it
// to the database, and returns a refreshed account pointer.
// For non-OAuth accounts (imap_smtp) it is a no-op.
func (s *Scheduler) ensureFreshToken(account *models.EmailAccount) *models.EmailAccount {
	if account.Provider != models.ProviderGmail && account.Provider != models.ProviderOutlook && account.Provider != models.ProviderOutlookPersonal {
		return account
	}
	// Force refresh if Outlook token is opaque (not a JWT — doesn't contain dots).
	// Opaque tokens (EwAYBOl3... format) are v1.0 tokens that IMAP rejects.
	// A valid IMAP token is a 3-part JWT: header.payload.signature
	isOpaque := account.Provider == models.ProviderOutlook &&
		strings.Count(account.AccessToken, ".") < 2
	if !auth.IsTokenExpired(account.TokenExpiry) && !isOpaque {
		return account
	}
	if isOpaque {
		logger.Debug("[oauth:%s] opaque v1 token detected — forcing refresh to get JWT", account.EmailAddress)
	}
	if account.RefreshToken == "" {
		logger.Debug("[oauth:%s] token expired but no refresh token stored — re-authorisation required", account.EmailAddress)
		return account
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	accessTok, refreshTok, expiry, err := auth.RefreshAccountToken(
		ctx,
		string(account.Provider),
		account.RefreshToken,
		s.cfg.BaseURL,
		s.cfg.GoogleClientID, s.cfg.GoogleClientSecret,
		s.cfg.MicrosoftClientID, s.cfg.MicrosoftClientSecret, s.cfg.MicrosoftTenantID,
	)
	if err != nil {
		logger.Debug("[oauth:%s] token refresh failed: %v", account.EmailAddress, err)
		s.db.SetAccountError(account.ID, "OAuth token refresh failed: "+err.Error())
		return account // return original; connect will fail and log the error
	}
	if err := s.db.UpdateAccountTokens(account.ID, accessTok, refreshTok, expiry); err != nil {
		logger.Debug("[oauth:%s] failed to persist refreshed token: %v", account.EmailAddress, err)
		return account
	}

	// Re-fetch so the caller gets the updated access token from the DB.
	refreshed, fetchErr := s.db.GetAccount(account.ID)
	if fetchErr != nil || refreshed == nil {
		return account
	}
	logger.Debug("[oauth:%s] access token refreshed (expires %s)", account.EmailAddress, expiry.Format("2006-01-02 15:04 UTC"))
	return refreshed
}

// ---- Public API (called by HTTP handlers) ----

// SyncAccountNow performs an immediate delta sync of one account.
func (s *Scheduler) SyncAccountNow(accountID int64) (int, error) {
	account, err := s.db.GetAccount(accountID)
	if err != nil || account == nil {
		return 0, fmt.Errorf("account %d not found", accountID)
	}
	s.drainPendingOps(account)
	s.deltaSync(account)
	return 0, nil
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

	// Graph accounts use the Graph sync path, not IMAP
	if account.Provider == models.ProviderOutlookPersonal {
		account = s.ensureFreshToken(account)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		gc := graph.New(account)
		// Force full resync of this folder by ignoring the since filter
		msgs, err := gc.ListMessages(ctx, folder.FullPath, time.Time{}, 100)
		if err != nil {
			return 0, fmt.Errorf("graph list messages: %w", err)
		}
		n := 0
		for _, gm := range msgs {
			msg := &models.Message{
				AccountID:     account.ID,
				FolderID:      folder.ID,
				RemoteUID:     gm.ID,
				MessageID:     gm.InternetMessageID,
				Subject:       gm.Subject,
				FromName:      gm.FromName(),
				FromEmail:     gm.FromEmail(),
				ToList:        gm.ToList(),
				Date:          gm.ReceivedDateTime,
				IsRead:        gm.IsRead,
				IsStarred:     gm.IsFlagged(),
				HasAttachment: gm.HasAttachments,
			}
			if dbErr := s.db.UpsertMessage(msg); dbErr == nil {
				n++
			}
		}
		// Update folder counts
		s.db.UpdateFolderCountsDirect(folder.ID, len(msgs), 0)
		return n, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	account = s.ensureFreshToken(account)
	c, err := email.Connect(ctx, account)
	if err != nil {
		return 0, err
	}
	defer c.Close()

	return s.syncFolder(c, account, folder)
}

// ---- Microsoft Graph sync (personal outlook.com accounts) ----

// graphWorker is the accountWorker equivalent for ProviderOutlookPersonal accounts.
// It polls Graph API instead of using IMAP.
func (s *Scheduler) graphWorker(account *models.EmailAccount, stop chan struct{}, push chan struct{}) {
	logger.Debug("[graph] worker started for %s", account.EmailAddress)

	getAccount := func() *models.EmailAccount {
		a, _ := s.db.GetAccount(account.ID)
		if a == nil {
			return account
		}
		return a
	}

	// Initial sync
	s.graphDeltaSync(getAccount())

	syncTicker := time.NewTicker(30 * time.Second)
	defer syncTicker.Stop()

	for {
		select {
		case <-stop:
			logger.Debug("[graph] worker stopped for %s", account.EmailAddress)
			return
		case <-push:
			acc := getAccount()
			s.graphDeltaSync(acc)
		case <-syncTicker.C:
			acc := getAccount()
			// Respect sync interval
			if !acc.LastSync.IsZero() {
				interval := time.Duration(acc.SyncInterval) * time.Minute
				if interval <= 0 {
					interval = 15 * time.Minute
				}
				if time.Since(acc.LastSync) < interval {
					continue
				}
			}
			s.graphDeltaSync(acc)
		}
	}
}

// graphDeltaSync fetches mail via Graph API and stores it in the same DB tables
// as the IMAP sync path, so the rest of the app works unchanged.
func (s *Scheduler) graphDeltaSync(account *models.EmailAccount) {
	account = s.ensureFreshToken(account)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	gc := graph.New(account)

	// Fetch folders
	gFolders, err := gc.ListFolders(ctx)
	if err != nil {
		log.Printf("[graph:%s] list folders: %v", account.EmailAddress, err)
		s.db.SetAccountError(account.ID, "Graph API error: "+err.Error())
		return
	}
	s.db.ClearAccountError(account.ID)

	totalNew := 0
	for _, gf := range gFolders {
		folderType := graph.InferFolderType(gf.DisplayName)
		dbFolder := &models.Folder{
			AccountID:   account.ID,
			Name:        gf.DisplayName,
			FullPath:    gf.ID, // Graph uses opaque IDs as folder path
			FolderType:  folderType,
			UnreadCount: gf.UnreadCount,
			TotalCount:  gf.TotalCount,
			SyncEnabled: true,
		}
		if err := s.db.UpsertFolder(dbFolder); err != nil {
			continue
		}
		dbFolderSaved, _ := s.db.GetFolderByPath(account.ID, gf.ID)
		if dbFolderSaved == nil || !dbFolderSaved.SyncEnabled {
			continue
		}

		// Fetch latest messages — no since filter, rely on upsert idempotency.
		// Graph uses sentDateTime for sent items which differs from receivedDateTime,
		// making date-based filters unreliable across folder types.
		// Fetching top 100 newest per folder per sync is efficient enough.
		msgs, err := gc.ListMessages(ctx, gf.ID, time.Time{}, 100)
		if err != nil {
			log.Printf("[graph:%s] list messages in %s: %v", account.EmailAddress, gf.DisplayName, err)
			continue
		}

		for _, gm := range msgs {
			// Body is NOT included in list response — fetched lazily on first open via GetMessage.
			msg := &models.Message{
				AccountID:     account.ID,
				FolderID:      dbFolderSaved.ID,
				RemoteUID:     gm.ID,
				MessageID:     gm.InternetMessageID,
				Subject:       gm.Subject,
				FromName:      gm.FromName(),
				FromEmail:     gm.FromEmail(),
				ToList:        gm.ToList(),
				Date:          gm.ReceivedDateTime,
				IsRead:        gm.IsRead,
				IsStarred:     gm.IsFlagged(),
				HasAttachment: gm.HasAttachments,
			}
			if err := s.db.UpsertMessage(msg); err == nil {
				totalNew++
			}
		}

		// Update folder counts from Graph (more accurate than counting locally)
		s.db.UpdateFolderCountsDirect(dbFolderSaved.ID, gf.TotalCount, gf.UnreadCount)
	}

	s.db.UpdateAccountLastSync(account.ID)
	if totalNew > 0 {
		logger.Debug("[graph:%s] %d new messages", account.EmailAddress, totalNew)
	}
}
