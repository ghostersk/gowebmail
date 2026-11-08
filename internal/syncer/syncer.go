// Package syncer provides background IMAP synchronisation for all active accounts.
// Architecture:
//   - One goroutine per account runs IDLE on the INBOX to receive push notifications.
//   - A separate drain goroutine flushes pending_imap_ops (delete/move/flag writes).
//   - Periodic full-folder delta sync catches changes made by other clients.
package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/ghostersk/gowebmail/internal/logger"
	"github.com/ghostersk/gowebmail/config"
	"github.com/ghostersk/gowebmail/internal/auth"
	"github.com/ghostersk/gowebmail/internal/caldav"
	"github.com/ghostersk/gowebmail/internal/db"
	"github.com/ghostersk/gowebmail/internal/email"
	"github.com/ghostersk/gowebmail/internal/graph"
	"github.com/ghostersk/gowebmail/internal/jmap"
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

	// CalDAV/CardDAV sync is optional and independent of the mail provider above,
	// so it runs for every account regardless of which branch below is taken.
	// davWorker no-ops on each tick if neither URL is configured.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.davWorker(account, stop)
	}()

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

	// JMAP accounts use a different sync path (REST/JSON, like Graph)
	if account.Provider == models.ProviderJMAP {
		s.jmapWorker(account, stop, push)
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

	// Fetched once per folder-sync, not per message — rules rarely change mid-sync.
	activeRules, _ := s.db.ListActiveRules(account.ID)

	// UIDVALIDITY changed = folder was recreated on server; wipe local and re-fetch all
	if storedValidity != 0 && status.UIDValidity != storedValidity {
		log.Printf("[sync] UIDVALIDITY changed for %s/%s — full re-sync", account.EmailAddress, dbFolder.FullPath)
		s.db.DeleteAllFolderMessages(dbFolder.ID)
		lastSeenUID = 0
	}

	// 1. Fetch new messages (UID > lastSeenUID)
	isIncrementalSync := lastSeenUID != 0 // false = first-ever sync or post-UIDVALIDITY full re-sync
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

	// Collected only for a genuine incremental inbox sync — never for first-sync/full-resync
	// backfill (that's historical mail, not "new mail") — mirrors the same restraint the
	// reconciliation step below already applies to rules/spam-move side effects.
	var pushCandidates []*models.Message

	maxUID := lastSeenUID
	for _, msg := range msgs {
		msg.FolderID = dbFolder.ID
		if err := s.db.UpsertMessage(msg); err == nil {
			newMessages++
			// Save attachment metadata if any (enables download)
			if len(msg.Attachments) > 0 && msg.ID > 0 {
				_ = s.db.SaveAttachmentMeta(msg.ID, msg.Attachments)
			}
			if dbFolder.FolderType != "spam" && s.db.IsSpamBlocked(account.UserID, msg.FromEmail) {
				s.moveToSpamIMAP(account, dbFolder, msg)
			} else if rule := matchRule(msg, account.EmailAddress, activeRules); rule != nil {
				s.applyRuleIMAP(c, account, dbFolder, msg, rule)
			} else if isIncrementalSync && dbFolder.FolderType == "inbox" && !msg.IsRead {
				pushCandidates = append(pushCandidates, msg)
			}
		}
		uid := uint32(0)
		fmt.Sscanf(msg.RemoteUID, "%d", &uid)
		if uid > maxUID {
			maxUID = uid
		}
	}
	if len(pushCandidates) > 0 {
		s.sendNewMailPush(account.UserID, pushCandidates)
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

		// 4. Reconcile the other direction: any UID the server has that we don't (from any
		// past cause of local data loss — a bug, a crash mid-write, manual intervention) is
		// re-fetched here, so the local cache always self-heals back to matching the server
		// instead of staying permanently drifted — the incremental fetch in step 1 alone can
		// never recover these, since it only ever asks for UIDs newer than last_seen_uid.
		if localUIDs, lerr := s.db.GetLocalUIDSet(dbFolder.ID); lerr == nil {
			var missing []uint32
			for _, uid := range serverUIDs {
				if !localUIDs[fmt.Sprintf("%d", uid)] {
					missing = append(missing, uid)
				}
			}
			if len(missing) > 0 {
				recovered, rerr := c.FetchByUIDs(dbFolder.FullPath, missing)
				if rerr != nil {
					log.Printf("[sync] recover missing %s/%s: %v", account.EmailAddress, dbFolder.FullPath, rerr)
				} else {
					n := 0
					for _, msg := range recovered {
						msg.FolderID = dbFolder.ID
						if dbErr := s.db.UpsertMessage(msg); dbErr == nil {
							n++
							if len(msg.Attachments) > 0 && msg.ID > 0 {
								_ = s.db.SaveAttachmentMeta(msg.ID, msg.Attachments)
							}
						}
					}
					if n > 0 {
						log.Printf("[sync] recovered %d message(s) missing from local cache in %s/%s", n, account.EmailAddress, dbFolder.FullPath)
						newMessages += n
					}
				}
			}
		}
	}

	// Save sync state
	s.db.SetFolderSyncState(dbFolder.ID, status.UIDValidity, maxUID)

	// Count from local rows so the sidebar always matches what the message list shows.
	// (Server STATUS counts can diverge — e.g. freebede auto-saves Sent mail twice,
	// inflating the server total beyond the deduped local rows.)
	s.db.UpdateFolderCounts(dbFolder.ID)

	return newMessages, nil
}

// sendNewMailPush delivers a background Web Push notification for genuinely new unread
// inbox mail to every device userID has subscribed (Settings > General > Notifications).
// Mirrors the title/body convention already used client-side by sendOSNotification() in
// app.js so a background push and a foreground toast read the same way.
func (s *Scheduler) sendNewMailPush(userID int64, msgs []*models.Message) {
	if s.cfg.VAPIDPrivateKey == "" || s.cfg.VAPIDPublicKey == "" {
		return
	}
	subs, err := s.db.GetPushSubscriptionsForUser(userID)
	if err != nil || len(subs) == 0 {
		return
	}

	first := msgs[0]
	fromLabel := first.FromName
	if fromLabel == "" {
		fromLabel = first.FromEmail
	}
	subject := first.Subject
	if subject == "" {
		subject = "(no subject)"
	}

	title, body := fromLabel, subject
	if len(msgs) > 1 {
		title = fmt.Sprintf("%d new messages in GoWebMail", len(msgs))
		body = fmt.Sprintf("%s: %s", fromLabel, subject)
	}

	payload, err := json.Marshal(map[string]string{"title": title, "body": body, "tag": "gowebmail-new"})
	if err != nil {
		return
	}

	for _, sub := range subs {
		resp, err := webpush.SendNotification(payload, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
		}, &webpush.Options{
			Subscriber:      "mailto:noreply@" + s.cfg.Hostname,
			VAPIDPublicKey:  s.cfg.VAPIDPublicKey,
			VAPIDPrivateKey: s.cfg.VAPIDPrivateKey,
			TTL:             60,
		})
		if err != nil {
			log.Printf("[push] send to user %d: %v", userID, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			// Subscription is dead (browser data cleared, app uninstalled, etc).
			s.db.DeletePushSubscriptionByEndpoint(sub.Endpoint)
		}
	}
}

// ---- Pending ops drain ----
// Applies queued IMAP write operations (delete/move/flag) with retry logic.

func (s *Scheduler) drainPendingOps(account *models.EmailAccount) {
	// Graph/JMAP accounts don't use the IMAP ops queue — their mutations are
	// applied synchronously in the API handlers instead (see api.go).
	if account.Provider == models.ProviderOutlookPersonal || account.Provider == models.ProviderJMAP {
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
			if abandoned := s.db.IncrementPendingOpAttempts(op.ID); abandoned {
				log.Printf("[ops:%s] giving up on %s uid=%d folder=%s after repeated failures: %v", account.EmailAddress, op.OpType, op.RemoteUID, op.FolderPath, applyErr)
				s.db.SetAccountError(account.ID, fmt.Sprintf("a %s operation failed repeatedly and was abandoned: %v", op.OpType, applyErr))
			}
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
	switch account.Provider {
	case models.ProviderOutlookPersonal:
		s.graphDeltaSync(account)
	case models.ProviderJMAP:
		s.jmapDeltaSync(account)
	default:
		s.deltaSync(account)
	}
	s.davSync(account)
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
		// Count from local rows so sidebar matches the message list
		s.db.UpdateFolderCounts(folder.ID)
		return n, nil
	}

	// JMAP accounts use the JMAP sync path, not IMAP
	if account.Provider == models.ProviderJMAP {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		jc := jmap.New(account.IMAPHost, account.EmailAddress, account.AccessToken)
		msgs, err := jc.ListEmails(ctx, folder.FullPath, 100)
		if err != nil {
			return 0, fmt.Errorf("jmap list emails: %w", err)
		}
		n := 0
		for _, jm := range msgs {
			msg := &models.Message{
				AccountID:     account.ID,
				FolderID:      folder.ID,
				RemoteUID:     jm.ID,
				Subject:       jm.Subject,
				FromName:      jm.FromName(),
				FromEmail:     jm.FromEmail(),
				ToList:        jm.ToList(),
				Date:          jm.ReceivedAt,
				IsRead:        jm.IsRead(),
				IsStarred:     jm.IsFlagged(),
				HasAttachment: jm.HasAttachment,
			}
			if dbErr := s.db.UpsertMessage(msg); dbErr == nil {
				n++
			}
		}
		s.db.UpdateFolderCounts(folder.ID)
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

		// Fetched once per folder-sync, not per message.
		activeRules, _ := s.db.ListActiveRules(account.ID)

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
				// NOTE: msg.BodyText is never populated here (body is fetched lazily on open,
				// by design, for perf) — a rule's "body" condition never matches on this path.
				if dbFolderSaved.FolderType != "spam" && s.db.IsSpamBlocked(account.UserID, msg.FromEmail) {
					s.moveToSpamGraph(gc, account, msg)
				} else if rule := matchRule(msg, account.EmailAddress, activeRules); rule != nil {
					s.applyRuleGraph(gc, account, msg, rule)
				}
			}
		}

		// Count from local rows so sidebar matches the message list
		s.db.UpdateFolderCounts(dbFolderSaved.ID)
	}

	s.db.UpdateAccountLastSync(account.ID)
	if totalNew > 0 {
		logger.Debug("[graph:%s] %d new messages", account.EmailAddress, totalNew)
	}
}

// ---- JMAP sync ----
// jmapWorker is the accountWorker equivalent for ProviderJMAP accounts. It
// polls the JMAP server instead of using IMAP — mirrors graphWorker, since
// both are REST/JSON providers with no IMAP-style IDLE connection to hold open.

func (s *Scheduler) jmapWorker(account *models.EmailAccount, stop chan struct{}, push chan struct{}) {
	logger.Debug("[jmap] worker started for %s", account.EmailAddress)

	getAccount := func() *models.EmailAccount {
		a, _ := s.db.GetAccount(account.ID)
		if a == nil {
			return account
		}
		return a
	}

	s.jmapDeltaSync(getAccount())

	syncTicker := time.NewTicker(30 * time.Second)
	defer syncTicker.Stop()

	for {
		select {
		case <-stop:
			logger.Debug("[jmap] worker stopped for %s", account.EmailAddress)
			return
		case <-push:
			s.jmapDeltaSync(getAccount())
		case <-syncTicker.C:
			acc := getAccount()
			if !acc.LastSync.IsZero() {
				interval := time.Duration(acc.SyncInterval) * time.Minute
				if interval <= 0 {
					interval = 15 * time.Minute
				}
				if time.Since(acc.LastSync) < interval {
					continue
				}
			}
			s.jmapDeltaSync(acc)
		}
	}
}

// jmapDeltaSync fetches mail via JMAP and stores it in the same DB tables as
// the IMAP/Graph sync paths, so the rest of the app works unchanged.
// account.IMAPHost holds the JMAP server base URL (see models.EmailAccount).
func (s *Scheduler) jmapDeltaSync(account *models.EmailAccount) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	jc := jmap.New(account.IMAPHost, account.EmailAddress, account.AccessToken)

	boxes, err := jc.ListMailboxes(ctx)
	if err != nil {
		log.Printf("[jmap:%s] list mailboxes: %v", account.EmailAddress, err)
		s.db.SetAccountError(account.ID, "JMAP error: "+err.Error())
		return
	}
	s.db.ClearAccountError(account.ID)

	totalNew := 0
	for _, mb := range boxes {
		folderType := jmap.InferFolderType(mb.Role)
		dbFolder := &models.Folder{
			AccountID:   account.ID,
			Name:        mb.Name,
			FullPath:    mb.ID, // JMAP uses opaque IDs as folder path, like Graph
			FolderType:  folderType,
			UnreadCount: mb.UnreadEmails,
			TotalCount:  mb.TotalEmails,
			SyncEnabled: true,
		}
		if err := s.db.UpsertFolder(dbFolder); err != nil {
			continue
		}
		dbFolderSaved, _ := s.db.GetFolderByPath(account.ID, mb.ID)
		if dbFolderSaved == nil || !dbFolderSaved.SyncEnabled {
			continue
		}

		// Fetch latest messages — no since filter, rely on upsert idempotency,
		// same approach as graphDeltaSync (JMAP's Email/query sort isn't
		// documented as supported — see tests/jmap-client.md).
		msgs, err := jc.ListEmails(ctx, mb.ID, 100)
		if err != nil {
			log.Printf("[jmap:%s] list emails in %s: %v", account.EmailAddress, mb.Name, err)
			continue
		}

		for _, jm := range msgs {
			// Body is NOT included in list response — fetched lazily on first
			// open, same as Graph's lazy-body pattern.
			msg := &models.Message{
				AccountID:     account.ID,
				FolderID:      dbFolderSaved.ID,
				RemoteUID:     jm.ID,
				Subject:       jm.Subject,
				FromName:      jm.FromName(),
				FromEmail:     jm.FromEmail(),
				ToList:        jm.ToList(),
				Date:          jm.ReceivedAt,
				IsRead:        jm.IsRead(),
				IsStarred:     jm.IsFlagged(),
				HasAttachment: jm.HasAttachment,
			}
			if err := s.db.UpsertMessage(msg); err == nil {
				totalNew++
			}
		}

		s.db.UpdateFolderCounts(dbFolderSaved.ID)
	}

	s.db.UpdateAccountLastSync(account.ID)
	if totalNew > 0 {
		logger.Debug("[jmap:%s] %d new messages", account.EmailAddress, totalNew)
	}
}

// ---- CalDAV/CardDAV sync ----
// Optional per-account add-on, independent of the mail provider (IMAP/JMAP/Graph).
// Pull-only: mirrors the remote calendar/address book into the local DB.

func (s *Scheduler) davWorker(account *models.EmailAccount, stop chan struct{}) {
	getAccount := func() *models.EmailAccount {
		a, _ := s.db.GetAccount(account.ID)
		if a == nil {
			return account
		}
		return a
	}

	s.davSync(getAccount())

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.davSync(getAccount())
		}
	}
}

func (s *Scheduler) davSync(account *models.EmailAccount) {
	if account.CalDAVURL == "" && account.CardDAVURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if account.CalDAVURL != "" {
		events, err := caldav.SyncCalendar(ctx, account.CalDAVURL, account.EmailAddress, account.AccessToken, account.ID)
		if err != nil {
			logger.Debug("[caldav:%s] sync: %v", account.EmailAddress, err)
		} else {
			uids := make([]string, 0, len(events))
			for _, e := range events {
				e.UserID = account.UserID
				if err := s.db.UpsertCalendarEvent(e); err == nil {
					uids = append(uids, e.UID)
				}
			}
			s.db.DeleteCalendarEventsNotIn(account.ID, uids)
		}
	}

	if account.CardDAVURL != "" {
		contacts, err := caldav.SyncContacts(ctx, account.CardDAVURL, account.EmailAddress, account.AccessToken, account.ID)
		if err != nil {
			logger.Debug("[carddav:%s] sync: %v", account.EmailAddress, err)
		} else {
			uids := make([]string, 0, len(contacts))
			for _, c := range contacts {
				c.UserID = account.UserID
				if err := s.db.UpsertContact(c); err == nil {
					uids = append(uids, c.UID)
				}
			}
			s.db.DeleteContactsNotIn(account.ID, uids)
		}
	}
}
