package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	goSync "sync"
	"time"

	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/carddav"
	"github.com/hkdb/aerion/internal/certificate"
	"github.com/hkdb/aerion/internal/contact"
	"github.com/hkdb/aerion/internal/credentials"
	"github.com/hkdb/aerion/internal/database"
	"github.com/hkdb/aerion/internal/draft"
	"github.com/hkdb/aerion/internal/folder"
	"github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/ipc"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/message"
	"github.com/hkdb/aerion/internal/oauth2"
	"github.com/hkdb/aerion/internal/platform"
	"github.com/hkdb/aerion/internal/settings"
	"github.com/hkdb/aerion/internal/pgp"
	"github.com/hkdb/aerion/internal/smime"
	"github.com/hkdb/aerion/internal/smtp"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ComposerConfig holds the configuration for a composer window.
type ComposerConfig struct {
	AccountID  string // Required: account to compose from
	IPCAddress string // Required: address of main window's IPC server
	Mode       string // "new", "reply", "reply-all", "forward"
	MessageID  string // Original message ID (for reply/forward)
	DraftID    string // Draft ID to resume editing
	MailtoURL  string // External mailto: URL to pre-fill (detached mode)
}

// ComposeMode represents the compose mode data returned to the frontend.
type ComposeMode struct {
	AccountID string `json:"accountId"`
	Mode      string `json:"mode"`
	MessageID string `json:"messageId"`
	DraftID   string `json:"draftId"`
}

// ComposerApp is a lightweight app struct for detached composer windows.
// It connects to the main window via IPC and shares the same database.
type ComposerApp struct {
	ctx    context.Context
	config ComposerConfig

	// Debug mode function reference (injected from main)
	debugMode func() bool

	// IPC client for communication with main window
	ipcClient ipc.Client
	ipcToken  string

	// Database (shared with main window, read-only for most operations)
	db            *database.DB
	accountStore  *account.Store
	folderStore   *folder.Store
	messageStore  *message.Store
	contactStore  *contact.Store
	draftStore    *draft.Store
	credStore     *credentials.Store
	certStore     *certificate.Store
	settingsStore *settings.Store

	// IMAP pool for sending/draft operations
	imapPool *imap.Pool

	// OAuth2 manager for token refresh
	oauth2Manager *oauth2.Manager

	// S/MIME signing, encryption, and decryption
	smimeStore     *smime.Store
	smimeSigner    *smime.Signer
	smimeEncryptor *smime.Encryptor
	smimeDecryptor *smime.Decryptor

	// PGP signing, encryption, and decryption
	pgpStore     *pgp.Store
	pgpSigner    *pgp.Signer
	pgpEncryptor *pgp.Encryptor
	pgpDecryptor *pgp.Decryptor

	// Shared draft operations
	draftOps draftOps

	// Shared compose operations
	composeOps composeOps

	// Paths
	paths *platform.Paths

	// Draft IMAP sync goroutine tracking
	draftSyncCancel context.CancelFunc
	draftSyncDone   chan struct{}
	draftSyncMu     goSync.Mutex

	// Composer state
	originalMessage *message.Message     // For reply/forward
	currentDraft    *draft.Draft         // Current draft being edited
	composeMessage  *smtp.ComposeMessage // Prepared compose message
}

// NewComposerApp creates a new ComposerApp with the given configuration.
func NewComposerApp(config ComposerConfig, debugModeFn func() bool) *ComposerApp {
	return &ComposerApp{
		config:    config,
		debugMode: debugModeFn,
	}
}

// Startup is called when the composer window starts.
func (c *ComposerApp) Startup(ctx context.Context) {
	c.ctx = ctx

	// Initialize logging - fatal only unless --debug flag is used
	logLevel := "fatal"
	if c.debugMode != nil && c.debugMode() {
		logLevel = "debug"
	}
	logging.Init(logging.Config{
		Level:   logLevel,
		Console: true,
	})
	log := logging.WithComponent("composer")

	log.Info().
		Str("accountID", c.config.AccountID).
		Str("mode", c.config.Mode).
		Str("ipcAddress", c.config.IPCAddress).
		Msg("Composer window starting")

	// Get platform paths
	paths, err := platform.GetPaths()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get platform paths")
	}
	c.paths = paths

	// Open database (shared with main window)
	db, err := database.Open(paths.DatabasePath())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open database")
	}
	c.db = db

	// Initialize stores
	c.accountStore = account.NewStore(db)
	c.folderStore = folder.NewStore(db)
	c.messageStore = message.NewStore(db)
	c.contactStore = contact.NewStore(db.DB)

	// Initialize vCard scanner for contact autocomplete (shared .vcf files)
	vcardScanner := contact.NewVCardScanner(contact.DefaultVCardPaths(), 20*time.Minute)
	c.contactStore.SetVCardScanner(vcardScanner)
	go vcardScanner.Scan()

	// Initialize CardDAV search for contact autocomplete (reads from shared DB)
	carddavStore := carddav.NewStore(db.DB)
	c.contactStore.SetCardDAVSearchFunc(func(query string, limit int) ([]*contact.Contact, error) {
		contacts, err := carddavStore.SearchContacts(query, limit)
		if err != nil {
			return nil, err
		}
		result := make([]*contact.Contact, len(contacts))
		for i, cdContact := range contacts {
			result[i] = &contact.Contact{
				Email:       cdContact.Email,
				DisplayName: cdContact.DisplayName,
				Source:      "carddav",
			}
		}
		return result, nil
	})

	c.draftStore = draft.NewStore(db)
	c.settingsStore = settings.NewStore(db)

	// Initialize credential store
	credStore, err := credentials.NewStore(db.DB, paths.Data)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize credential store")
	}
	c.credStore = credStore

	// Initialize certificate trust store (TOFU)
	c.certStore = certificate.NewStore(db.DB)

	// Initialize S/MIME store, signer, and encryptor
	c.smimeStore = smime.NewStore(db.DB, log)
	c.smimeSigner = smime.NewSigner(c.smimeStore, credStore, log)
	c.smimeEncryptor = smime.NewEncryptor(c.smimeStore, credStore, log)
	c.smimeDecryptor = smime.NewDecryptor(c.smimeStore, credStore, log)

	// Initialize PGP store, signer, and encryptor
	c.pgpStore = pgp.NewStore(db.DB, log)
	c.pgpSigner = pgp.NewSigner(c.pgpStore, credStore, log)
	c.pgpEncryptor = pgp.NewEncryptor(c.pgpStore, credStore, log)
	c.pgpDecryptor = pgp.NewDecryptor(c.pgpStore, credStore, log)

	// Initialize IMAP pool for send/draft operations
	poolConfig := imap.DefaultPoolConfig()
	poolConfig.MaxConnections = 1 // Composer only needs 1 connection
	c.imapPool = imap.NewPool(poolConfig, c.getIMAPCredentials)

	// Initialize shared draft operations
	c.draftOps = draftOps{
		accountStore:   c.accountStore,
		folderStore:    c.folderStore,
		messageStore:   c.messageStore,
		draftStore:     c.draftStore,
		imapPool:       c.imapPool,
		smimeSigner:    c.smimeSigner,
		smimeEncryptor: c.smimeEncryptor,
		smimeDecryptor: c.smimeDecryptor,
		pgpSigner:      c.pgpSigner,
		pgpEncryptor:   c.pgpEncryptor,
		pgpDecryptor:   c.pgpDecryptor,
	}

	// Initialize OAuth2 manager for token refresh
	c.oauth2Manager = oauth2.NewManager()

	// Initialize shared compose operations
	c.composeOps = composeOps{
		accountStore:   c.accountStore,
		folderStore:    c.folderStore,
		credStore:      c.credStore,
		certStore:      c.certStore,
		contactStore:   c.contactStore,
		oauth2Manager:  c.oauth2Manager,
		smimeStore:     c.smimeStore,
		smimeSigner:    c.smimeSigner,
		smimeEncryptor: c.smimeEncryptor,
		pgpStore:       c.pgpStore,
		pgpSigner:      c.pgpSigner,
		pgpEncryptor:   c.pgpEncryptor,
		draftOps:       &c.draftOps,
	}

	// Connect to main window's IPC server
	if err := c.connectIPC(ctx); err != nil {
		log.Error().Err(err).Msg("Failed to connect to IPC server")
		// Continue anyway - composer can still work offline
	}

	// Load initial data based on mode
	if err := c.loadInitialData(); err != nil {
		log.Error().Err(err).Msg("Failed to load initial data")
	}

	// Notify main window that we're ready
	c.notifyReady()

	log.Info().Msg("Composer window started successfully")
}

// Shutdown is called when the composer window is closing.
func (c *ComposerApp) Shutdown(ctx context.Context) {
	log := logging.WithComponent("composer")

	// Notify main window that we're closing
	c.notifyClosed()

	// Close IPC connection
	if c.ipcClient != nil {
		c.ipcClient.Close()
	}

	// Close IMAP connections
	if c.imapPool != nil {
		c.imapPool.CloseAll()
	}

	// Close database
	if c.db != nil {
		c.db.Close()
	}

	log.Info().Msg("Composer window shutdown complete")
}

// connectIPC establishes connection to the main window's IPC server.
func (c *ComposerApp) connectIPC(ctx context.Context) error {
	log := logging.WithComponent("composer.ipc")

	// Read token from stdin (passed by parent process)
	tokenBytes, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read token from stdin: %w", err)
	}
	c.ipcToken = strings.TrimSpace(string(tokenBytes))

	if c.ipcToken == "" {
		return fmt.Errorf("no token provided via stdin")
	}

	log.Debug().Str("address", c.config.IPCAddress).Msg("Connecting to IPC server")

	// Create IPC client
	c.ipcClient = ipc.NewClient(c.config.IPCAddress)

	// Register message handler before connecting
	c.ipcClient.OnMessage(c.handleIPCMessage)

	// Connect with token authentication
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := c.ipcClient.Connect(connectCtx, c.ipcToken); err != nil {
		return fmt.Errorf("failed to connect to IPC server: %w", err)
	}

	log.Info().Msg("Connected to IPC server")
	return nil
}

// handleIPCMessage processes messages received from the main window.
func (c *ComposerApp) handleIPCMessage(msg ipc.Message) {
	log := logging.WithComponent("composer.ipc")

	log.Debug().Str("type", msg.Type).Msg("Received IPC message")

	switch msg.Type {
	case ipc.TypeThemeChanged:
		var payload ipc.ThemeChangedPayload
		if err := msg.ParsePayload(&payload); err == nil {
			// Emit event to frontend
			wailsRuntime.EventsEmit(c.ctx, "theme:changed", payload.Theme)
		}

	case ipc.TypeAccountUpdated:
		var payload ipc.AccountUpdatedPayload
		if err := msg.ParsePayload(&payload); err == nil {
			// Forward account updates for any account (composer supports cross-account)
			wailsRuntime.EventsEmit(c.ctx, "account:updated", payload.AccountID)
		}

	case ipc.TypeContactsUpdated:
		// Emit event to frontend to refresh autocomplete
		wailsRuntime.EventsEmit(c.ctx, "contacts:updated", nil)

	case ipc.TypeShutdown:
		var payload ipc.ShutdownPayload
		msg.ParsePayload(&payload)
		log.Info().Str("reason", payload.Reason).Msg("Received shutdown request from main window")
		// Emit event to frontend to prompt user
		wailsRuntime.EventsEmit(c.ctx, "app:shutdown", payload.Reason)

	default:
		log.Debug().Str("type", msg.Type).Msg("Unknown IPC message type")
	}
}

// loadInitialData loads the initial data based on compose mode.
func (c *ComposerApp) loadInitialData() error {
	log := logging.WithComponent("composer")

	log.Debug().
		Str("draftID", c.config.DraftID).
		Str("messageID", c.config.MessageID).
		Str("mode", c.config.Mode).
		Msg("Loading initial data")

	// If resuming a draft, load it
	if c.config.DraftID != "" {
		draft, err := c.draftStore.Get(c.config.DraftID)
		if err != nil {
			log.Error().Err(err).Str("draftID", c.config.DraftID).Msg("Failed to get draft from store")
			return fmt.Errorf("failed to load draft: %w", err)
		}
		if draft != nil {
			c.currentDraft = draft
			log.Info().
				Str("draftID", c.config.DraftID).
				Str("subject", draft.Subject).
				Uint32("imapUID", draft.IMAPUID).
				Msg("Loaded draft into currentDraft")
		} else {
			log.Warn().Str("draftID", c.config.DraftID).Msg("Draft not found in database")
		}
		return nil
	}

	// If replying/forwarding, load the original message
	if c.config.MessageID != "" && c.config.Mode != "new" {
		msg, err := c.messageStore.Get(c.config.MessageID)
		if err != nil {
			return fmt.Errorf("failed to load original message: %w", err)
		}
		if msg != nil {
			c.originalMessage = msg
			log.Info().Str("messageID", c.config.MessageID).Str("mode", c.config.Mode).Msg("Loaded original message")
		}
	}

	return nil
}

// notifyReady sends a ready notification to the main window.
func (c *ComposerApp) notifyReady() {
	if c.ipcClient == nil {
		return
	}

	msg, err := ipc.NewMessage(ipc.TypeComposerReady, nil)
	if err != nil {
		return
	}
	c.ipcClient.Send(msg)
}

// notifyClosed sends a closed notification to the main window.
func (c *ComposerApp) notifyClosed() {
	if c.ipcClient == nil {
		return
	}

	var draftID *int64
	if c.currentDraft != nil {
		id, _ := parseIntID(c.currentDraft.ID)
		draftID = &id
	}

	msg, err := ipc.NewMessage(ipc.TypeComposerClosed, ipc.ComposerClosedPayload{
		DraftID: draftID,
	})
	if err != nil {
		return
	}
	c.ipcClient.Send(msg)
}

// notifyMessageSent sends a message-sent notification to the main window.
func (c *ComposerApp) notifyMessageSent(accountID string, folderID int64) {
	if c.ipcClient == nil {
		return
	}

	msg, err := ipc.NewMessage(ipc.TypeMessageSent, ipc.MessageSentPayload{
		AccountID: accountID,
		FolderID:  folderID,
	})
	if err != nil {
		return
	}
	c.ipcClient.Send(msg)
}

// notifyDraftSaved sends a draft-saved notification to the main window.
func (c *ComposerApp) notifyDraftSaved(accountID string, draftID string) {
	if c.ipcClient == nil {
		return
	}

	msg, err := ipc.NewMessage(ipc.TypeDraftSaved, ipc.DraftSavedPayload{
		AccountID: accountID,
		DraftID:   draftID,
	})
	if err != nil {
		return
	}
	c.ipcClient.Send(msg)
}

// notifyDraftDeleted sends a draft-deleted notification to the main window.
func (c *ComposerApp) notifyDraftDeleted(accountID string) {
	if c.ipcClient == nil {
		return
	}

	msg, err := ipc.NewMessage(ipc.TypeDraftDeleted, ipc.DraftDeletedPayload{
		AccountID: accountID,
	})
	if err != nil {
		return
	}
	c.ipcClient.Send(msg)
}

// getIMAPCredentials returns IMAP credentials for an account.
// Handles both password and OAuth2 authentication.
func (c *ComposerApp) getIMAPCredentials(accountID string) (*imap.ClientConfig, error) {
	return c.composeOps.getIMAPCredentials(c.ctx, accountID)
}

// getValidOAuthToken returns a valid OAuth token, refreshing if needed.
func (c *ComposerApp) getValidOAuthToken(accountID string) (*credentials.OAuthTokens, error) {
	return c.composeOps.getValidOAuthToken(c.ctx, accountID)
}

// ============================================================================
// Wails-bound methods (exposed to frontend)
// ============================================================================

// GetAccount returns the account for the given account ID.
func (c *ComposerApp) GetAccount(accountID string) (*account.Account, error) {
	return c.accountStore.Get(accountID)
}

// GetIdentities returns all identities for the given account.
func (c *ComposerApp) GetIdentities(accountID string) ([]*account.Identity, error) {
	return c.accountStore.GetIdentities(accountID)
}

// GetAllAccountIdentities returns all enabled accounts with their identities.
// Used by the detached composer to populate the cross-account From dropdown.
func (c *ComposerApp) GetAllAccountIdentities() ([]AccountIdentityGroup, error) {
	accounts, err := c.accountStore.List()
	if err != nil {
		return nil, err
	}
	var groups []AccountIdentityGroup
	for _, acc := range accounts {
		if !acc.Enabled {
			continue
		}
		identities, err := c.accountStore.GetIdentities(acc.ID)
		if err != nil {
			return nil, err
		}
		groups = append(groups, AccountIdentityGroup{
			Account:    acc,
			Identities: identities,
		})
	}
	return groups, nil
}

// GetComposeMode returns the compose mode and related data.
func (c *ComposerApp) GetComposeMode() *ComposeMode {
	return &ComposeMode{
		AccountID: c.config.AccountID,
		Mode:      c.config.Mode,
		MessageID: c.config.MessageID,
		DraftID:   c.config.DraftID,
	}
}

// GetShowTitleBar returns whether the custom title bar should be shown.
func (c *ComposerApp) GetShowTitleBar() (bool, error) {
	return c.settingsStore.GetShowTitleBar()
}

// GetNativeTitleBar returns whether the native OS title bar is enabled.
func (c *ComposerApp) GetNativeTitleBar() (bool, error) {
	return c.settingsStore.GetNativeTitleBar()
}

// GetThemeMode returns the current theme mode setting.
func (c *ComposerApp) GetThemeMode() (string, error) {
	return c.settingsStore.GetThemeMode()
}

// GetSystemTheme returns the current system theme preference detected via
// the XDG Settings Portal on Linux. Returns "light", "dark", or "" if not available.
func (c *ComposerApp) GetSystemTheme() string {
	return platform.ReadSystemTheme()
}

// IsFlatpak returns true if the application is running inside a Flatpak sandbox.
func (c *ComposerApp) IsFlatpak() bool {
	return platform.IsFlatpak()
}

// GetOriginalMessage returns the original message for reply/forward.
func (c *ComposerApp) GetOriginalMessage() (*message.Message, error) {
	if c.originalMessage != nil {
		return c.originalMessage, nil
	}
	if c.config.MessageID == "" {
		return nil, nil
	}
	return c.messageStore.Get(c.config.MessageID)
}

// GetDraft returns the current draft being edited.
func (c *ComposerApp) GetDraft() (*smtp.ComposeMessage, error) {
	if c.currentDraft == nil {
		return nil, nil
	}
	return c.draftToComposeMessage(c.currentDraft), nil
}

// PrepareReply builds a ComposeMessage for the current mode.
func (c *ComposerApp) PrepareReply() (*smtp.ComposeMessage, error) {
	if c.config.Mode == "new" || c.config.MessageID == "" {
		// New message - return compose, optionally pre-filled from mailto URL
		acc, err := c.accountStore.Get(c.config.AccountID)
		if err != nil {
			return nil, err
		}
		identities, _ := c.accountStore.GetIdentities(c.config.AccountID)

		var fromIdentity *account.Identity
		for _, id := range identities {
			if id.IsDefault {
				fromIdentity = id
				break
			}
		}
		if fromIdentity == nil && len(identities) > 0 {
			fromIdentity = identities[0]
		}

		from := smtp.Address{Address: acc.Email, Name: acc.Name}
		if fromIdentity != nil {
			from = smtp.Address{Address: fromIdentity.Email, Name: fromIdentity.Name}
		}

		msg := &smtp.ComposeMessage{
			From: from,
		}

		// Pre-fill from mailto URL if provided
		if c.config.MailtoURL != "" {
			mailtoData := ParseMailtoURL(c.config.MailtoURL)
			if mailtoData != nil {
				for _, addr := range mailtoData.To {
					msg.To = append(msg.To, smtp.Address{Address: addr})
				}
				for _, addr := range mailtoData.Cc {
					msg.Cc = append(msg.Cc, smtp.Address{Address: addr})
				}
				for _, addr := range mailtoData.Bcc {
					msg.Bcc = append(msg.Bcc, smtp.Address{Address: addr})
				}
				msg.Subject = mailtoData.Subject
				msg.TextBody = mailtoData.Body
			}
		}

		return msg, nil
	}

	// For reply/forward, use the same logic as main app
	// This is a simplified version - the full logic is in app.go PrepareReply
	msg, err := c.messageStore.Get(c.config.MessageID)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, fmt.Errorf("message not found: %s", c.config.MessageID)
	}

	c.originalMessage = msg
	c.composeMessage = c.buildReplyMessage(msg, c.config.Mode)
	return c.composeMessage, nil
}

// SearchContacts searches for contacts matching the query.
func (c *ComposerApp) SearchContacts(query string, limit int) ([]*contact.Contact, error) {
	return c.contactStore.Search(query, limit)
}

// SendMessage sends the composed email.
func (c *ComposerApp) SendMessage(accountID string, msg smtp.ComposeMessage) error {
	// Cancel in-flight draft sync before send+delete
	if c.currentDraft != nil {
		c.cancelDraftSync()
	}

	_, err := c.composeOps.sendMessage(c.ctx, accountID, msg, c.currentDraft)
	if err != nil {
		return err
	}

	// Post-send: IPC notification + state cleanup
	if c.currentDraft != nil {
		c.notifyDraftDeleted(accountID)
		c.currentDraft = nil
	}

	// Get sent folder ID for notification
	sentFolder, _ := c.folderStore.GetByType(accountID, folder.TypeSent)
	var sentFolderID int64
	if sentFolder != nil {
		sentFolderID, _ = parseIntID(sentFolder.ID)
	}

	// Notify main window
	c.notifyMessageSent(accountID, sentFolderID)

	return nil
}

// saveToSentFolder appends the sent message to the Sent folder via IMAP.
func (c *ComposerApp) saveToSentFolder(accountID string, acc *account.Account, rawMsg []byte) error {
	return c.composeOps.saveToSentFolder(c.ctx, accountID, acc, rawMsg)
}

// cancelDraftSync cancels any in-flight syncDraftToIMAP goroutine and waits for
// it to finish. This prevents the race where DeleteDraft runs while a background
// goroutine is still uploading the draft to IMAP.
func (c *ComposerApp) cancelDraftSync() {
	c.draftSyncMu.Lock()
	cancel := c.draftSyncCancel
	done := c.draftSyncDone
	c.draftSyncMu.Unlock()

	if cancel == nil {
		return
	}
	cancel()
	if done == nil {
		return
	}
	<-done
}

// SaveDraft saves the current compose state as a draft.
// If existingDraftID is provided, updates that draft instead of creating a new one.
func (c *ComposerApp) SaveDraft(accountID string, msg smtp.ComposeMessage, existingDraftID string) (*draft.Draft, error) {
	log := logging.WithComponent("composer")

	log.Debug().
		Str("existingDraftID", existingDraftID).
		Str("subject", msg.Subject).
		Msg("Saving draft")

	var localDraft *draft.Draft

	// Try to load existing draft if ID provided
	if existingDraftID != "" {
		existing, err := c.draftStore.Get(existingDraftID)
		if err != nil {
			log.Warn().Err(err).Str("draftID", existingDraftID).Msg("Failed to load existing draft from ID")
		}
		if err == nil && existing != nil {
			localDraft = existing
			log.Debug().Str("draftID", existingDraftID).Msg("Loaded existing draft from provided ID")
		}
	}

	// Fall back to c.currentDraft if no draft loaded yet
	if localDraft == nil && c.currentDraft != nil {
		localDraft = c.currentDraft
		log.Debug().Str("draftID", localDraft.ID).Msg("Using c.currentDraft")
	}

	enc, err := c.draftOps.encryptDraftBody(accountID, msg.From.Address, msg)
	if err != nil {
		return nil, err
	}

	localDraft, err = c.draftOps.saveDraftToDB(accountID, localDraft, msg, enc)
	if err != nil {
		return nil, err
	}

	// Keep c.currentDraft in sync
	c.currentDraft = localDraft

	// Cancel any previous in-flight sync before starting a new one
	c.cancelDraftSync()

	// Sync to IMAP in background with cancellation support
	ctx, cancel := context.WithCancel(c.ctx)
	done := make(chan struct{})
	c.draftSyncMu.Lock()
	c.draftSyncCancel = cancel
	c.draftSyncDone = done
	c.draftSyncMu.Unlock()

	go func() {
		defer recoverPanic("composer", "sync draft to IMAP")
		defer close(done)
		defer func() {
			c.draftSyncMu.Lock()
			if c.draftSyncDone == done {
				c.draftSyncCancel = nil
				c.draftSyncDone = nil
			}
			c.draftSyncMu.Unlock()
		}()
		c.syncDraftToIMAP(ctx, localDraft, msg)
	}()

	log.Info().Str("draftID", localDraft.ID).Bool("encrypted", enc.encrypted).Bool("pgpEncrypted", enc.pgpEncrypted).Msg("Draft saved")
	return localDraft, nil
}

// DeleteDraft deletes a draft from local DB and IMAP.
// If draftID is empty, falls back to c.currentDraft.ID or c.config.DraftID.
func (c *ComposerApp) DeleteDraft(draftID string) error {
	log := logging.WithComponent("composer")

	// Determine which draft ID to use
	if draftID == "" && c.currentDraft != nil {
		draftID = c.currentDraft.ID
	}
	if draftID == "" && c.config.DraftID != "" {
		draftID = c.config.DraftID
	}

	if draftID == "" {
		log.Debug().Msg("No draft ID provided, nothing to delete")
		return nil
	}

	log.Debug().Str("draftID", draftID).Msg("DeleteDraft called")

	// Cancel any in-flight IMAP sync goroutine and wait for it to finish.
	// This ensures the goroutine can't upload the draft after we delete it.
	c.cancelDraftSync()

	// Load the draft directly from database (re-read after cancel to get latest state)
	draftToDelete, err := c.draftStore.Get(draftID)
	if err != nil {
		log.Warn().Err(err).Str("draftID", draftID).Msg("Failed to load draft for deletion")
		return fmt.Errorf("failed to load draft: %w", err)
	}
	if draftToDelete == nil {
		log.Debug().Str("draftID", draftID).Msg("Draft not found in database, nothing to delete")
		return nil
	}

	log.Info().
		Str("draftID", draftToDelete.ID).
		Uint32("imapUID", draftToDelete.IMAPUID).
		Str("syncStatus", string(draftToDelete.SyncStatus)).
		Msg("Deleting draft")

	_, err = c.draftOps.deleteDraftCore(c.ctx, draftToDelete)
	if err != nil {
		return err
	}

	// Notify main window to refresh Drafts folder
	c.notifyDraftDeleted(draftToDelete.AccountID)

	log.Info().Str("draftID", draftToDelete.ID).Msg("Draft deleted successfully")

	// Clear currentDraft if it matches
	if c.currentDraft != nil && c.currentDraft.ID == draftToDelete.ID {
		c.currentDraft = nil
	}

	return nil
}

// syncDraftToIMAP syncs a draft to the IMAP server.
// This runs in a background goroutine and emits events to this window's frontend.
func (c *ComposerApp) syncDraftToIMAP(ctx context.Context, localDraft *draft.Draft, msg smtp.ComposeMessage) {
	emitStatus := func(status draft.SyncStatus, imapUID uint32, syncError string) {
		wailsRuntime.EventsEmit(c.ctx, "draft:syncStatusChanged", map[string]interface{}{
			"draftId":    localDraft.ID,
			"syncStatus": status,
			"imapUid":    imapUID,
			"error":      syncError,
		})
	}

	draftsFolder := c.draftOps.syncToIMAP(ctx, localDraft, msg, emitStatus)
	if draftsFolder == nil {
		return
	}

	// Notify main window now that the draft is on IMAP
	// This triggers the main window to sync the Drafts folder
	c.notifyDraftSaved(localDraft.AccountID, localDraft.ID)
}

// CloseWindow requests the window to close.
func (c *ComposerApp) CloseWindow() {
	wailsRuntime.Quit(c.ctx)
}

// PickAttachmentFiles opens a file picker dialog and returns the selected files as attachments.
func (c *ComposerApp) PickAttachmentFiles() ([]ComposerAttachment, error) {
	return pickAttachmentFiles(c.ctx)
}

// ReadFileAsAttachment reads a file from a filesystem path and creates a ComposerAttachment.
func (c *ComposerApp) ReadFileAsAttachment(filePath string) (*ComposerAttachment, error) {
	return readFileAsAttachment(filePath)
}

// ============================================================================
// Helper methods
// ============================================================================

// draftToComposeMessage converts a draft to a ComposeMessage.
// If the draft is encrypted (S/MIME or PGP), decrypts the body first.
func (c *ComposerApp) draftToComposeMessage(d *draft.Draft) *smtp.ComposeMessage {
	return c.draftOps.toComposeMessage(d)
}

// buildReplyMessage builds a compose message for reply/forward.
// This is a simplified version of the logic in app.go PrepareReply.
func (c *ComposerApp) buildReplyMessage(msg *message.Message, mode string) *smtp.ComposeMessage {
	// Get default identity
	identities, _ := c.accountStore.GetIdentities(c.config.AccountID)
	var fromIdentity *account.Identity
	for _, id := range identities {
		if id.IsDefault {
			fromIdentity = id
			break
		}
	}
	if fromIdentity == nil && len(identities) > 0 {
		fromIdentity = identities[0]
	}

	from := smtp.Address{}
	if fromIdentity != nil {
		from = smtp.Address{Name: fromIdentity.Name, Address: fromIdentity.Email}
	}

	// Build subject
	subject := msg.Subject
	switch mode {
	case "forward":
		if !strings.HasPrefix(strings.ToLower(subject), "fwd:") && !strings.HasPrefix(strings.ToLower(subject), "fw:") {
			subject = "Fwd: " + subject
		}
	default: // reply, reply-all
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}
	}

	// Build recipients
	var to, cc []smtp.Address
	selfEmails := make(map[string]bool)
	for _, id := range identities {
		selfEmails[strings.ToLower(id.Email)] = true
	}

	originalFrom := []smtp.Address{{Name: msg.FromName, Address: msg.FromEmail}}

	switch mode {
	case "reply":
		to = filterSelfAddresses(originalFrom, selfEmails)
	case "reply-all":
		to = filterSelfAddresses(originalFrom, selfEmails)
		// Add original To (excluding self)
		originalTo := parseAddressList(msg.ToList)
		to = append(to, filterSelfAddresses(originalTo, selfEmails)...)
		// Add original Cc (excluding self and duplicates)
		originalCc := parseAddressList(msg.CcList)
		toSet := make(map[string]bool)
		for _, addr := range to {
			toSet[strings.ToLower(addr.Address)] = true
		}
		for _, addr := range filterSelfAddresses(originalCc, selfEmails) {
			if !toSet[strings.ToLower(addr.Address)] {
				cc = append(cc, addr)
			}
		}
	case "forward":
		// Leave empty for user to fill
	}

	// Build quoted body
	dateStr := msg.Date.Format("Mon, Jan 2 2006 at 3:04:05 PM MST")
	sender := msg.FromEmail
	if msg.FromName != "" {
		sender = msg.FromName + " <" + msg.FromEmail + ">"
	}

	var htmlBody, textBody string
	if mode == "forward" {
		htmlBody = fmt.Sprintf("<br><br>---------- Forwarded message ----------<br>From: %s<br>Subject: %s<br>Date: %s<br>To: %s<br><br>%s",
			escapeHTML(sender), escapeHTML(msg.Subject), escapeHTML(dateStr), escapeHTML(msg.ToList), msg.BodyHTML)
		textBody = fmt.Sprintf("\n\n---------- Forwarded message ----------\nFrom: %s\nSubject: %s\nDate: %s\nTo: %s\n\n%s",
			sender, msg.Subject, dateStr, msg.ToList, msg.BodyText)
	} else {
		citation := fmt.Sprintf("On %s, %s wrote:", dateStr, sender)
		htmlBody = fmt.Sprintf("<br><br>%s<br><blockquote type=\"cite\">%s</blockquote>", escapeHTML(citation), msg.BodyHTML)
		textBody = fmt.Sprintf("\n\n%s\n%s", citation, quoteText(msg.BodyText))
	}

	return &smtp.ComposeMessage{
		From:      from,
		To:        to,
		Cc:        cc,
		Subject:   subject,
		HTMLBody:  htmlBody,
		TextBody:  textBody,
		InReplyTo: msg.MessageID,
	}
}

// HasSMIMECertificate returns whether the account has a valid default S/MIME certificate.
func (c *ComposerApp) HasSMIMECertificate(accountID string) bool {
	return c.composeOps.hasSMIMECertificate(accountID)
}

// GetSMIMECertificateForEmail returns the S/MIME certificate matching the given email.
// Returns nil if no matching certificate is found.
func (c *ComposerApp) GetSMIMECertificateForEmail(accountID string, email string) (*smime.Certificate, error) {
	cert, _, err := c.smimeStore.GetCertificateByEmail(accountID, email)
	if err != nil {
		return nil, fmt.Errorf("failed to get certificate for email: %w", err)
	}
	return cert, nil
}

// GetSMIMESignPolicy returns the signing policy for the account.
func (c *ComposerApp) GetSMIMESignPolicy(accountID string) (string, error) {
	return c.smimeStore.GetSignPolicy(accountID)
}

// GetSMIMEEncryptPolicy returns the encryption policy for the account.
func (c *ComposerApp) GetSMIMEEncryptPolicy(accountID string) (string, error) {
	return c.smimeStore.GetEncryptPolicy(accountID)
}

// CheckRecipientCerts checks which recipients have S/MIME certificates available.
func (c *ComposerApp) CheckRecipientCerts(emails []string) (map[string]bool, error) {
	certPEMs, err := c.smimeStore.GetSenderCertPEMs(emails)
	if err != nil {
		return nil, fmt.Errorf("failed to check recipient certs: %w", err)
	}

	result := make(map[string]bool)
	for _, email := range emails {
		_, hasCert := certPEMs[email]
		result[email] = hasCert
	}
	return result, nil
}

// PickRecipientCertFile opens a file picker for certificate files.
func (c *ComposerApp) PickRecipientCertFile() (string, error) {
	path, err := wailsRuntime.OpenFileDialog(c.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Recipient Certificate",
		Filters: []wailsRuntime.FileFilter{
			{
				DisplayName: "Certificate Files (*.pem, *.cer, *.crt, *.der)",
				Pattern:     "*.pem;*.cer;*.crt;*.der",
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to open file dialog: %w", err)
	}
	return path, nil
}

// ImportRecipientCert imports a recipient's public certificate from a file.
func (c *ComposerApp) ImportRecipientCert(email, filePath string) error {
	if filePath == "" {
		return fmt.Errorf("no file selected")
	}
	if email == "" {
		return fmt.Errorf("email address required")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read certificate file: %w", err)
	}

	return c.smimeStore.ImportSenderCertFromFile(email, data)
}

// HasPGPKey returns whether the account has a valid default PGP key.
func (c *ComposerApp) HasPGPKey(accountID string) bool {
	return c.composeOps.hasPGPKey(accountID)
}

// GetPGPKeyForEmail returns the PGP key matching the given email.
// Returns nil if no matching key is found.
func (c *ComposerApp) GetPGPKeyForEmail(accountID string, email string) (*pgp.Key, error) {
	key, _, err := c.pgpStore.GetKeyByEmail(accountID, email)
	if err != nil {
		return nil, fmt.Errorf("failed to get key for email: %w", err)
	}
	return key, nil
}

// GetPGPSignPolicy returns the PGP signing policy for the account.
func (c *ComposerApp) GetPGPSignPolicy(accountID string) (string, error) {
	return c.pgpStore.GetSignPolicy(accountID)
}

// GetPGPEncryptPolicy returns the PGP encryption policy for the account.
func (c *ComposerApp) GetPGPEncryptPolicy(accountID string) (string, error) {
	return c.pgpStore.GetEncryptPolicy(accountID)
}

// CheckRecipientPGPKeys checks which recipients have PGP public keys available.
func (c *ComposerApp) CheckRecipientPGPKeys(emails []string) (map[string]bool, error) {
	armoredKeys, err := c.pgpStore.GetSenderKeyArmoreds(emails)
	if err != nil {
		return nil, fmt.Errorf("failed to check recipient PGP keys: %w", err)
	}

	result := make(map[string]bool)
	for _, email := range emails {
		_, hasKey := armoredKeys[email]
		result[email] = hasKey
	}
	return result, nil
}

// PickRecipientPGPKeyFile opens a file picker for PGP public key files.
func (c *ComposerApp) PickRecipientPGPKeyFile() (string, error) {
	path, err := wailsRuntime.OpenFileDialog(c.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Recipient PGP Public Key",
		Filters: []wailsRuntime.FileFilter{
			{
				DisplayName: "PGP Key Files (*.asc, *.gpg, *.key, *.pub)",
				Pattern:     "*.asc;*.gpg;*.key;*.pub",
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to open file dialog: %w", err)
	}
	return path, nil
}

// ImportRecipientPGPKey imports a recipient's PGP public key from a file.
func (c *ComposerApp) ImportRecipientPGPKey(email, filePath string) error {
	if filePath == "" {
		return fmt.Errorf("no file selected")
	}
	if email == "" {
		return fmt.Errorf("email address required")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read key file: %w", err)
	}

	return c.pgpStore.ImportSenderKeyFromFile(email, data)
}

// LookupWKD performs a Web Key Directory lookup for the given email address.
func (c *ComposerApp) LookupWKD(email string) (string, error) {
	armored, err := pgp.LookupWKD(email)
	if err != nil {
		return "", fmt.Errorf("WKD lookup failed: %w", err)
	}
	if armored == "" {
		return "", nil
	}

	// Cache the discovered key
	if err := c.pgpStore.CacheSenderKey(email, armored, "wkd"); err != nil {
		return armored, nil
	}

	return armored, nil
}

// LookupHKP performs an HKP key server lookup for the given email address.
func (c *ComposerApp) LookupHKP(email string) (string, error) {
	armored, err := pgp.LookupHKP(email, c.getHKPServers())
	if err != nil {
		return "", fmt.Errorf("HKP lookup failed: %w", err)
	}
	if armored == "" {
		return "", nil
	}

	if err := c.pgpStore.CacheSenderKey(email, armored, "hkp"); err != nil {
		return armored, nil
	}

	return armored, nil
}

// LookupPGPKey performs a unified WKD+HKP lookup for the given email address.
func (c *ComposerApp) LookupPGPKey(email string) (string, error) {
	result, err := pgp.LookupKey(email, c.getHKPServers())
	if err != nil {
		return "", fmt.Errorf("PGP key lookup failed: %w", err)
	}
	if result == nil {
		return "", nil
	}

	if err := c.pgpStore.CacheSenderKey(email, result.Armored, result.Source); err != nil {
		return result.Armored, nil
	}

	return result.Armored, nil
}

// getHKPServers reads configured key servers from the database table.
// Falls back to DefaultHKPServers if the table is empty.
func (c *ComposerApp) getHKPServers() []string {
	servers, err := c.pgpStore.ListKeyServers()
	if err != nil || len(servers) == 0 {
		return pgp.DefaultHKPServers
	}

	urls := make([]string, len(servers))
	for i, s := range servers {
		urls[i] = s.URL
	}
	return urls
}

// shouldPGPSignMessage determines whether a message should be PGP signed.
func (c *ComposerApp) shouldPGPSignMessage(perMessageOverride bool) bool {
	return c.composeOps.shouldPGPSignMessage(c.config.AccountID, perMessageOverride)
}

// shouldPGPEncryptMessage determines whether a message should be PGP encrypted.
func (c *ComposerApp) shouldPGPEncryptMessage(perMessageOverride bool) bool {
	return c.composeOps.shouldPGPEncryptMessage(c.config.AccountID, perMessageOverride)
}

// shouldSignMessage determines whether a message should be S/MIME signed.
func (c *ComposerApp) shouldSignMessage(perMessageOverride bool) bool {
	return c.composeOps.shouldSignMessage(c.config.AccountID, perMessageOverride)
}

// shouldEncryptMessage determines whether a message should be S/MIME encrypted.
func (c *ComposerApp) shouldEncryptMessage(perMessageOverride bool) bool {
	return c.composeOps.shouldEncryptMessage(c.config.AccountID, perMessageOverride)
}

// getDraftIdentityEmail returns the email address for the draft's identity.
// Falls back to the account email if the identity cannot be resolved.
func (c *ComposerApp) getDraftIdentityEmail(d *draft.Draft) string {
	return c.draftOps.getIdentityEmail(d)
}

// parseIntID parses a string ID to int64.
func parseIntID(id string) (int64, error) {
	var result int64
	_, err := fmt.Sscanf(id, "%d", &result)
	return result, err
}
