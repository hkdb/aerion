package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	goImap "github.com/emersion/go-imap/v2"
	"github.com/hkdb/aerion/internal/account"
	"github.com/hkdb/aerion/internal/certificate"
	"github.com/hkdb/aerion/internal/contact"
	"github.com/hkdb/aerion/internal/credentials"
	"github.com/hkdb/aerion/internal/draft"
	"github.com/hkdb/aerion/internal/folder"
	"github.com/hkdb/aerion/internal/email"
	"github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/message"
	"github.com/rs/zerolog"
	"github.com/hkdb/aerion/internal/oauth2"
	"github.com/hkdb/aerion/internal/pgp"
	"github.com/hkdb/aerion/internal/smime"
	"github.com/hkdb/aerion/internal/smtp"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ComposerAttachment represents an attachment in the compose window
type ComposerAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
	Data        string `json:"data"` // Base64 encoded
}

// composeOps holds shared dependencies for compose-related operations
// used by both App and ComposerApp.
type composeOps struct {
	accountStore   *account.Store
	folderStore    *folder.Store
	credStore      *credentials.Store
	certStore      *certificate.Store
	contactStore   *contact.Store
	oauth2Manager  *oauth2.Manager
	smimeStore     *smime.Store
	smimeSigner    *smime.Signer
	smimeEncryptor *smime.Encryptor
	pgpStore       *pgp.Store
	pgpSigner      *pgp.Signer
	pgpEncryptor   *pgp.Encryptor
	draftOps       *draftOps // for draft cleanup on send
}

// getValidOAuthToken returns a valid OAuth token, refreshing if needed.
// ctx is the caller's Wails context (for EventsEmit on reauth).
func (ops *composeOps) getValidOAuthToken(ctx context.Context, accountID string) (*credentials.OAuthTokens, error) {
	log := logging.WithComponent("composeOps")

	tokens, err := ops.credStore.GetOAuthTokens(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get OAuth tokens: %w", err)
	}

	// Check if token expires within 5 minutes
	if !tokens.IsExpiringSoon(5 * time.Minute) {
		return tokens, nil
	}

	log.Debug().
		Str("account_id", accountID).
		Time("expires_at", tokens.ExpiresAt).
		Msg("OAuth token expiring soon, refreshing")

	// Refresh the token
	newTokenResp, err := ops.oauth2Manager.RefreshToken(tokens.Provider, tokens.RefreshToken)
	if err != nil {
		log.Error().Err(err).
			Str("account_id", accountID).
			Msg("OAuth token refresh failed")

		// Emit event for frontend to prompt re-authorization
		wailsRuntime.EventsEmit(ctx, "oauth:reauth-required", map[string]interface{}{
			"accountId": accountID,
			"provider":  tokens.Provider,
			"error":     err.Error(),
		})

		return nil, fmt.Errorf("OAuth token refresh failed, re-authorization required: %w", err)
	}

	// Calculate new expiry time
	expiresAt := time.Now().Add(time.Duration(newTokenResp.ExpiresIn) * time.Second)

	// Update tokens in store
	tokens.AccessToken = newTokenResp.AccessToken
	tokens.ExpiresAt = expiresAt
	if newTokenResp.RefreshToken != "" {
		tokens.RefreshToken = newTokenResp.RefreshToken
	}

	if err := ops.credStore.SetOAuthTokens(accountID, tokens); err != nil {
		log.Warn().Err(err).Msg("Failed to save refreshed OAuth tokens")
		// Continue anyway - we have valid tokens in memory
	}

	log.Info().
		Str("account_id", accountID).
		Time("new_expires_at", expiresAt).
		Msg("OAuth token refreshed successfully")

	return tokens, nil
}

// getIMAPCredentials returns IMAP credentials for an account.
// Handles both password and OAuth2 authentication.
func (ops *composeOps) getIMAPCredentials(ctx context.Context, accountID string) (*imap.ClientConfig, error) {
	acc, err := ops.accountStore.Get(accountID)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}

	config := imap.DefaultConfig()
	config.Host = acc.IMAPHost
	config.Port = acc.IMAPPort
	config.Security = imap.SecurityType(acc.IMAPSecurity)
	config.Username = acc.Username
	config.TLSConfig = certificate.BuildTLSConfig(acc.IMAPHost, ops.certStore)

	switch acc.AuthType {
	case account.AuthOAuth2:
		tokens, err := ops.getValidOAuthToken(ctx, accountID)
		if err != nil {
			return nil, fmt.Errorf("failed to get OAuth token: %w", err)
		}
		config.AuthType = imap.AuthTypeOAuth2
		config.AccessToken = tokens.AccessToken
	default:
		password, err := ops.credStore.GetPassword(accountID)
		if err != nil {
			return nil, fmt.Errorf("failed to get password: %w", err)
		}
		config.AuthType = imap.AuthTypePassword
		config.Password = password
	}

	return &config, nil
}

// getSpecialFolder resolves a special folder for an account, checking the
// user's explicit mapping first, then falling back to auto-detected type.
// Mirrors App.GetSpecialFolder (app/folder.go) for use within composeOps.
func (ops *composeOps) getSpecialFolder(accountID string, folderType folder.Type) (*folder.Folder, error) {
	acc, err := ops.accountStore.Get(accountID)
	if err != nil || acc == nil {
		return ops.folderStore.GetByType(accountID, folderType)
	}
	mappedPath := acc.GetFolderMapping(string(folderType))
	if mappedPath != "" {
		f, err := ops.folderStore.GetByPath(accountID, mappedPath)
		if err == nil && f != nil {
			return f, nil
		}
	}
	return ops.folderStore.GetByType(accountID, folderType)
}

// saveToSentFolder appends the sent message to the Sent folder via IMAP.
func (ops *composeOps) saveToSentFolder(ctx context.Context, accountID string, acc *account.Account, rawMsg []byte) error {
	log := logging.WithComponent("composeOps")

	// Resolve the Sent folder using the same logic as App.GetSpecialFolder
	sentFolder, err := ops.getSpecialFolder(accountID, folder.TypeSent)
	if err != nil || sentFolder == nil {
		return fmt.Errorf("no Sent folder configured or detected")
	}
	sentPath := sentFolder.Path

	log.Debug().
		Str("account_id", accountID).
		Str("sent_path", sentPath).
		Msg("Saving sent message to folder via IMAP APPEND")

	// Create IMAP client
	clientConfig, err := ops.getIMAPCredentials(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get IMAP credentials: %w", err)
	}

	imapClient := imap.NewClient(*clientConfig)
	if err := imapClient.Connect(); err != nil {
		return fmt.Errorf("failed to connect to IMAP: %w", err)
	}
	defer imapClient.Close()

	if err := imapClient.Login(); err != nil {
		return fmt.Errorf("failed to login to IMAP: %w", err)
	}

	// Append message with \Seen flag
	flags := []goImap.Flag{goImap.FlagSeen}
	_, err = imapClient.AppendMessage(sentPath, flags, time.Now(), rawMsg)
	if err != nil {
		return fmt.Errorf("failed to append to Sent folder: %w", err)
	}

	log.Info().
		Str("account_id", accountID).
		Str("sent_path", sentPath).
		Msg("Message saved to Sent folder")

	return nil
}

// hasSMIMECertificate returns whether the account has a valid default S/MIME certificate.
func (ops *composeOps) hasSMIMECertificate(accountID string) bool {
	cert, _, err := ops.smimeStore.GetDefaultCertificate(accountID)
	return err == nil && cert != nil && !cert.IsExpired
}

// hasPGPKey returns whether the account has a valid default PGP key.
func (ops *composeOps) hasPGPKey(accountID string) bool {
	key, _, err := ops.pgpStore.GetDefaultKey(accountID)
	return err == nil && key != nil && !key.IsExpired
}

// shouldSignMessage determines whether a message should be S/MIME signed.
func (ops *composeOps) shouldSignMessage(accountID string, perMessageOverride bool) bool {
	if perMessageOverride {
		return ops.hasSMIMECertificate(accountID)
	}
	policy, err := ops.smimeStore.GetSignPolicy(accountID)
	if err != nil || policy != "always" {
		return false
	}
	return ops.hasSMIMECertificate(accountID)
}

// shouldEncryptMessage determines whether a message should be S/MIME encrypted.
func (ops *composeOps) shouldEncryptMessage(accountID string, perMessageOverride bool) bool {
	if perMessageOverride {
		return ops.hasSMIMECertificate(accountID)
	}
	policy, err := ops.smimeStore.GetEncryptPolicy(accountID)
	if err != nil || policy != "always" {
		return false
	}
	return ops.hasSMIMECertificate(accountID)
}

// shouldPGPSignMessage determines whether a message should be PGP signed.
func (ops *composeOps) shouldPGPSignMessage(accountID string, perMessageOverride bool) bool {
	if perMessageOverride {
		return ops.hasPGPKey(accountID)
	}
	policy, err := ops.pgpStore.GetSignPolicy(accountID)
	if err != nil || policy != "always" {
		return false
	}
	return ops.hasPGPKey(accountID)
}

// shouldPGPEncryptMessage determines whether a message should be PGP encrypted.
func (ops *composeOps) shouldPGPEncryptMessage(accountID string, perMessageOverride bool) bool {
	if perMessageOverride {
		return ops.hasPGPKey(accountID)
	}
	policy, err := ops.pgpStore.GetEncryptPolicy(accountID)
	if err != nil || policy != "always" {
		return false
	}
	return ops.hasPGPKey(accountID)
}

// sendMessage performs the full send flow: build RFC822, sign, encrypt, SMTP send,
// save to Sent folder, add recipients to contacts, and optionally delete a draft.
// When d is non-nil, the draft is fully cleaned up (IMAP + message row + DB) after
// a successful send. Returns the account for callers that need it post-send.
func (ops *composeOps) sendMessage(ctx context.Context, accountID string, msg smtp.ComposeMessage, d *draft.Draft) (*account.Account, error) {
	log := logging.WithComponent("composeOps")

	log.Info().
		Str("accountID", accountID).
		Str("from", msg.From.Address).
		Int("toCount", len(msg.To)).
		Str("subject", msg.Subject).
		Msg("Sending message")

	// Get account
	acc, err := ops.accountStore.Get(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	if acc == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}

	// Receive-only accounts have no SMTP wiring; reject sends before any
	// further work. The composer's From dropdown already filters these
	// out, so reaching this branch indicates a stale draft or a programmatic
	// caller — surface a clear error rather than dialing a non-existent
	// SMTP server.
	if acc.NoOutgoingServer {
		return nil, fmt.Errorf("account %q is configured as receive-only (no outgoing server)", acc.Email)
	}

	// Build RFC822 message
	rawMsg, err := msg.ToRFC822()
	if err != nil {
		return nil, fmt.Errorf("failed to build message: %w", err)
	}

	fromEmail := msg.From.Address

	// S/MIME signing (if configured for this account/message)
	if ops.shouldSignMessage(accountID, msg.SignMessage) {
		signedMsg, signErr := ops.smimeSigner.SignMessage(accountID, fromEmail, rawMsg)
		if signErr != nil {
			return nil, fmt.Errorf("failed to sign message: %w", signErr)
		}
		rawMsg = signedMsg
		log.Info().Str("accountID", accountID).Msg("Message signed with S/MIME")
	}

	// S/MIME encryption (if configured) — sign-then-encrypt per RFC 5751
	if ops.shouldEncryptMessage(accountID, msg.EncryptMessage) {
		encryptedMsg, encErr := ops.smimeEncryptor.EncryptMessage(accountID, fromEmail, msg.AllRecipients(), rawMsg)
		if encErr != nil {
			return nil, fmt.Errorf("failed to encrypt message: %w", encErr)
		}
		rawMsg = encryptedMsg
		log.Info().Str("accountID", accountID).Msg("Message encrypted with S/MIME")
	}

	// PGP signing (mutually exclusive with S/MIME — only if S/MIME sign was not applied)
	if !msg.SignMessage && ops.shouldPGPSignMessage(accountID, msg.PGPSignMessage) {
		signedMsg, signErr := ops.pgpSigner.SignMessage(accountID, fromEmail, rawMsg)
		if signErr != nil {
			return nil, fmt.Errorf("failed to PGP sign message: %w", signErr)
		}
		rawMsg = signedMsg
		log.Info().Str("accountID", accountID).Msg("Message signed with PGP")
	}

	// PGP encryption (mutually exclusive with S/MIME — only if S/MIME encrypt was not applied)
	if !msg.EncryptMessage && ops.shouldPGPEncryptMessage(accountID, msg.PGPEncryptMessage) {
		encryptedMsg, encErr := ops.pgpEncryptor.EncryptMessage(accountID, fromEmail, msg.AllRecipients(), rawMsg)
		if encErr != nil {
			return nil, fmt.Errorf("failed to PGP encrypt message: %w", encErr)
		}
		rawMsg = encryptedMsg
		log.Info().Str("accountID", accountID).Msg("Message encrypted with PGP")
	}

	// Create SMTP client config
	smtpConfig := smtp.DefaultConfig()
	smtpConfig.Host = acc.SMTPHost
	smtpConfig.Port = acc.SMTPPort
	smtpConfig.Security = smtp.SecurityType(acc.SMTPSecurity)
	smtpConfig.Username = acc.Username
	// Shared mailboxes authenticate SMTP with the parent account's username
	if acc.SharedMailboxParentID != "" {
		parent, parentErr := ops.accountStore.Get(acc.SharedMailboxParentID)
		if parentErr == nil && parent != nil {
			smtpConfig.Username = parent.Username
		}
	}
	// Separate SMTP credentials override (Generic provider; gated by
	// non-empty SMTPUsername at the model layer). Doesn't apply to OAuth
	// accounts — those keep the bearer-token path below.
	smtpUsesSeparateCreds := acc.SMTPUsername != "" && acc.AuthType != account.AuthOAuth2
	if smtpUsesSeparateCreds {
		smtpConfig.Username = acc.SMTPUsername
	}
	smtpConfig.TLSConfig = certificate.BuildTLSConfig(acc.SMTPHost, ops.certStore)

	// Handle authentication based on auth type
	switch acc.AuthType {
	case account.AuthOAuth2:
		tokens, tokenErr := ops.getValidOAuthToken(ctx, accountID)
		if tokenErr != nil {
			return nil, fmt.Errorf("failed to get OAuth token: %w", tokenErr)
		}
		smtpConfig.AuthType = smtp.AuthTypeOAuth2
		smtpConfig.AccessToken = tokens.AccessToken
	default:
		smtpConfig.AuthType = smtp.AuthTypePassword
		if smtpUsesSeparateCreds {
			password, passErr := ops.credStore.GetSMTPPassword(accountID)
			if passErr != nil {
				return nil, fmt.Errorf("failed to get SMTP password: %w", passErr)
			}
			smtpConfig.Password = password
			break
		}
		password, passErr := ops.credStore.GetPassword(accountID)
		if passErr != nil {
			return nil, fmt.Errorf("failed to get password: %w", passErr)
		}
		smtpConfig.Password = password
	}

	client := smtp.NewClient(smtpConfig)

	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer client.Close()

	if err := client.Login(); err != nil {
		return nil, fmt.Errorf("failed to login to SMTP server: %w", err)
	}

	recipients := msg.AllRecipients()
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients specified")
	}

	if err := client.SendMail(msg.From.Address, recipients, rawMsg); err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Save to Sent folder (using IMAP APPEND) if provider doesn't auto-save
	if !providerAutoSavesSentMail(acc.IMAPHost) {
		log.Debug().Str("host", acc.IMAPHost).Msg("Provider doesn't auto-save, using IMAP APPEND")
		if err := ops.saveToSentFolder(ctx, accountID, acc, rawMsg); err != nil {
			log.Warn().Err(err).Msg("Failed to save message to Sent folder")
			// Don't fail the send operation if saving fails
		}
	}

	// Add recipients to local contacts (best-effort; send already succeeded)
	for _, to := range msg.To {
		_ = ops.contactStore.AddOrUpdate(to.Address, to.Name)
	}
	for _, cc := range msg.Cc {
		_ = ops.contactStore.AddOrUpdate(cc.Address, cc.Name)
	}

	// Delete draft if one was provided (send already succeeded — log errors, don't fail)
	if d != nil && ops.draftOps != nil {
		if _, delErr := ops.draftOps.deleteDraftCore(ctx, d); delErr != nil {
			log.Warn().Err(delErr).Str("draftID", d.ID).Msg("Failed to delete draft after send")
		}
	}

	log.Info().Str("accountID", accountID).Msg("Message sent successfully")
	return acc, nil
}

// readFileAsAttachment reads a file and creates a ComposerAttachment.
func readFileAsAttachment(filePath string) (*ComposerAttachment, error) {
	log := logging.WithComponent("compose")

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	filename := filepath.Base(filePath)
	contentType := detectContentType(filename)
	encoded := base64.StdEncoding.EncodeToString(content)

	log.Debug().
		Str("filename", filename).
		Str("contentType", contentType).
		Int("size", len(content)).
		Msg("File read as attachment")

	return &ComposerAttachment{
		Filename:    filename,
		ContentType: contentType,
		Size:        len(content),
		Data:        encoded,
	}, nil
}

// pickAttachmentFiles opens a file picker dialog and returns the selected files as attachments.
func pickAttachmentFiles(ctx context.Context) ([]ComposerAttachment, error) {
	log := logging.WithComponent("compose")

	files, err := wailsRuntime.OpenMultipleFilesDialog(ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Attachments",
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to show file picker dialog")
		return nil, fmt.Errorf("failed to show file picker: %w", err)
	}

	if len(files) == 0 {
		return nil, nil
	}

	var attachments []ComposerAttachment
	for _, filePath := range files {
		att, err := readFileAsAttachment(filePath)
		if err != nil {
			log.Warn().Err(err).Str("path", filePath).Msg("Failed to read file as attachment")
			continue
		}
		attachments = append(attachments, *att)
	}

	log.Info().Int("count", len(attachments)).Msg("Files picked for attachment")
	return attachments, nil
}

// ============================================================================
// Compose API - Exposed to frontend via Wails bindings
// ============================================================================

// SendMessage sends an email via SMTP.
// The message is composed in the frontend and sent to the backend.
func (a *App) SendMessage(accountID string, msg smtp.ComposeMessage) error {
	_, err := a.composeOps.sendMessage(a.ctx, accountID, msg, nil)
	if err != nil {
		return err
	}
	go func() {
		if err := a.syncSentFolder(accountID); err != nil {
			log := logging.WithComponent("app")
			log.Debug().Err(err).Str("accountID", accountID).Msg("Sent folder sync failed")
		}
	}()
	return nil
}

// handleExternalMailto handles a mailto URL received from a second instance.
// Routes to inline or detached composer based on the mailto_mode setting.
func (a *App) handleExternalMailto(rawURL string) {
	log := logging.WithComponent("app")

	mailtoData := ParseMailtoURL(rawURL)
	if mailtoData == nil {
		log.Warn().Str("url", rawURL).Msg("Invalid mailto URL from second instance")
		return
	}

	mailtoMode, _ := a.settingsStore.GetMailtoMode()
	log.Info().Str("mode", mailtoMode).Msg("Handling external mailto")

	if mailtoMode == "detached" {
		// Pick first account for detached composer
		accounts, err := a.accountStore.List()
		if err != nil || len(accounts) == 0 {
			log.Warn().Msg("No accounts available for mailto")
			return
		}
		if err := a.OpenComposerWindow(accounts[0].ID, "new", "", "", rawURL); err != nil {
			log.Warn().Err(err).Msg("Failed to open detached composer for mailto")
		}
		return
	}

	// Inline mode: show window and emit event for frontend
	a.ShowWindow()
	wailsRuntime.EventsEmit(a.ctx, "mailto:external", mailtoData)
}

// syncSentFolder syncs the Sent folder for an account after sending a message
func (a *App) syncSentFolder(accountID string) error {
	defer recoverPanic("app.compose", "sync sent folder")
	log := logging.WithComponent("app")

	sentFolder, err := a.GetSpecialFolder(accountID, folder.TypeSent)
	if err != nil || sentFolder == nil {
		log.Warn().Str("accountID", accountID).Msg("Could not find Sent folder for sync")
		return nil
	}

	// Get account to determine sync period
	acc, _ := a.accountStore.Get(accountID)
	syncPeriodDays := 30 // default
	if acc != nil {
		syncPeriodDays = acc.SyncPeriodDays
	}

	// Sync progress feedback rides on the syncEngine's sync:progress callback (see app.go:438)
	if err := a.syncEngine.SyncMessages(a.ctx, accountID, sentFolder.ID, syncPeriodDays); err != nil {
		log.Warn().Err(err).Str("folderID", sentFolder.ID).Msg("Failed to sync Sent folder")
	}

	// Also fetch bodies
	if err := a.syncEngine.FetchBodiesInBackground(a.ctx, accountID, sentFolder.ID, syncPeriodDays); err != nil {
		log.Warn().Err(err).Str("folderID", sentFolder.ID).Msg("Failed to fetch bodies for Sent folder")
	}

	// Emit synced event
	wailsRuntime.EventsEmit(a.ctx, "folder:synced", map[string]interface{}{
		"accountId": accountID,
		"folderId":  sentFolder.ID,
	})

	// Notify conversation viewer that sent folder synced (for cross-folder thread refresh)
	wailsRuntime.EventsEmit(a.ctx, "sent:synced", map[string]interface{}{
		"accountId": accountID,
	})

	return nil
}

// PrepareReply prepares a reply message structure from an existing message.
// mode can be "reply", "reply-all", or "forward"
func (a *App) PrepareReply(messageID, mode string) (*smtp.ComposeMessage, error) {
	log := logging.WithComponent("app")
	log.Debug().Str("messageID", messageID).Str("mode", mode).Msg("Preparing reply message")

	// Get the original message
	msg, err := a.messageStore.Get(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	if msg == nil {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	// Get account and identities
	identities, err := a.accountStore.GetIdentities(msg.AccountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get identities: %w", err)
	}

	// Find the default identity or first identity
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
	if fromIdentity == nil {
		acc, _ := a.accountStore.Get(msg.AccountID)
		if acc != nil {
			fromIdentity = &account.Identity{
				Email: acc.Email,
				Name:  acc.Name,
			}
		}
	}

	// Build the From address
	from := smtp.Address{}
	if fromIdentity != nil {
		from = smtp.Address{Name: fromIdentity.Name, Address: fromIdentity.Email}
	}

	// Build subject with Re: or Fwd: prefix
	subject := msg.Subject
	switch mode {
	case "forward":
		if !strings.HasPrefix(strings.ToLower(subject), "fwd:") && !strings.HasPrefix(strings.ToLower(subject), "fw:") {
			subject = "Fwd: " + subject
		}
	case "reply", "reply-all":
		if !strings.HasPrefix(strings.ToLower(subject), "re:") {
			subject = "Re: " + subject
		}
	}

	// Build the To and Cc lists based on mode
	var to, cc []smtp.Address
	selfEmails := make(map[string]bool)
	for _, id := range identities {
		selfEmails[strings.ToLower(strings.TrimSpace(id.Email))] = true
	}

	// Prefer Reply-To over From per RFC 5322
	originalFrom := []smtp.Address{{Name: msg.FromName, Address: strings.TrimSpace(msg.FromEmail)}}
	if msg.ReplyTo != "" {
		originalFrom = []smtp.Address{{Address: strings.TrimSpace(msg.ReplyTo)}}
	}

	switch mode {
	case "reply":
		// Reply to sender only
		to = filterSelfAddresses(originalFrom, selfEmails)

		// Defensive fix: if reply resulted in no recipients (replying to self),
		// include the original sender anyway
		if len(to) == 0 && len(originalFrom) > 0 {
			to = originalFrom
		}
	case "reply-all":
		// Reply to sender
		to = filterSelfAddresses(originalFrom, selfEmails)
		// Include original To recipients (excluding self)
		originalTo := parseAddressList(msg.ToList)
		to = append(to, filterSelfAddresses(originalTo, selfEmails)...)
		// Include original Cc recipients (excluding self and duplicates from To)
		originalCc := parseAddressList(msg.CcList)
		toSet := make(map[string]bool)
		for _, addr := range to {
			toSet[strings.ToLower(strings.TrimSpace(addr.Address))] = true
		}
		for _, addr := range filterSelfAddresses(originalCc, selfEmails) {
			if !toSet[strings.ToLower(strings.TrimSpace(addr.Address))] {
				cc = append(cc, addr)
			}
		}

		// Defensive fix: if reply-all resulted in no recipients at all,
		// include the original sender even if it matches a self email
		// (this can happen when replying to your own sent messages)
		if len(to) == 0 && len(cc) == 0 && len(originalFrom) > 0 {
			to = originalFrom
		}
	case "forward":
		// Leave To empty for user to fill in
	}

	// If body hasn't been fetched yet, fetch it on-demand
	if !msg.BodyFetched {
		log.Debug().Str("messageID", messageID).Msg("Body not yet fetched, fetching on-demand for reply")
		updatedMsg, fetchErr := a.syncEngine.FetchMessageBody(a.ctx, msg.AccountID, messageID)
		if fetchErr != nil {
			log.Warn().Err(fetchErr).Msg("Failed to fetch body on-demand, reply will have empty quote")
		} else if updatedMsg != nil {
			msg = updatedMsg
		}
	}

	// Build the quoted body
	dateStr := msg.Date.Format("Mon, Jan 2 2006 at 3:04:05 PM MST")
	sender := msg.FromEmail
	if msg.FromName != "" {
		sender = msg.FromName + " <" + msg.FromEmail + ">"
	}

	// For plain-text-only emails, convert text to HTML for quoting
	quotedHTML := msg.BodyHTML
	if quotedHTML == "" && msg.BodyText != "" {
		quotedHTML = "<p>" + strings.ReplaceAll(escapeHTML(msg.BodyText), "\n", "<br>") + "</p>"
	}

	// Block remote images in quoted content to prevent tracking pixels.
	// Original URLs are preserved in data-original-src for restoration on send.
	// The frontend can unblock them if the sender is in the image allowlist.
	quotedHTML = email.BlockRemoteImages(quotedHTML)

	// Plaintext quote source: prefer the original's text part, but fall back to
	// deriving it from HTML so HTML-only originals still produce a real quote
	// (msg.BodyText is empty for HTML-only mail). Consumed by the plaintext composer.
	quotedText := msg.BodyText
	if strings.TrimSpace(quotedText) == "" && msg.BodyHTML != "" {
		quotedText = email.ExtractPlainTextFromHTML(msg.BodyHTML)
	}

	var htmlBody, textBody string
	if mode == "forward" {
		// Forward format
		htmlBody = fmt.Sprintf("<p></p><p></p><p>---------- Forwarded message ----------<br>From: %s<br>Subject: %s<br>Date: %s<br>To: %s</p><p></p>%s",
			escapeHTML(sender), escapeHTML(msg.Subject), escapeHTML(dateStr), escapeHTML(msg.ToList), quotedHTML)
		textBody = fmt.Sprintf("\n\n---------- Forwarded message ----------\nFrom: %s\nSubject: %s\nDate: %s\nTo: %s\n\n%s",
			sender, msg.Subject, dateStr, msg.ToList, quotedText)
	} else {
		// Reply format
		citation := fmt.Sprintf("On %s, %s wrote:", dateStr, sender)
		htmlBody = fmt.Sprintf("<p></p><p></p><p>%s</p><blockquote type=\"cite\">%s</blockquote>", escapeHTML(citation), quotedHTML)
		textBody = fmt.Sprintf("\n\n%s\n%s", citation, quoteText(quotedText))
	}

	// Build References header per RFC 5322:
	// References = parent's References + parent's Message-ID
	var refs []string
	if msg.References != "" {
		// References are stored as a JSON array in the DB
		_ = json.Unmarshal([]byte(msg.References), &refs)
	}
	if msg.MessageID != "" {
		refs = append(refs, ensureAngleBrackets(msg.MessageID))
	}

	// Fetch inline attachments so cid: references in quoted HTML render correctly
	var attachments []smtp.Attachment
	inlineMap, inlineErr := a.attachmentStore.GetInlineByMessage(messageID)
	if inlineErr != nil {
		log.Warn().Err(inlineErr).Msg("Failed to get inline attachments for reply/forward")
	}
	for cid, dataURL := range inlineMap {
		ct, b64 := parseDataURL(dataURL)
		if b64 == "" {
			continue
		}
		attachments = append(attachments, smtp.Attachment{
			ContentBase64: b64,
			ContentType:   ct,
			ContentID:     cid,
			Inline:        true,
			Filename:      cid,
		})
	}

	// For forwards, also include regular (non-inline) attachments
	if mode == "forward" {
		a.fetchForwardAttachments(log, msg, &attachments)
	}

	return &smtp.ComposeMessage{
		From:        from,
		To:          to,
		Cc:          cc,
		Subject:     subject,
		HTMLBody:    htmlBody,
		TextBody:    textBody,
		InReplyTo:   ensureAngleBrackets(msg.MessageID),
		References:  refs,
		Attachments: attachments,
	}, nil
}

// fetchForwardAttachments fetches regular (non-inline) attachment content from IMAP
// and appends them to the attachments slice. Failures are logged but not fatal.
func (a *App) fetchForwardAttachments(log zerolog.Logger, msg *message.Message, attachments *[]smtp.Attachment) {
	allAtts, err := a.attachmentStore.GetByMessage(msg.ID)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get attachments for forward")
		return
	}

	// Filter to non-inline attachments only
	var regularAtts []*message.Attachment
	for _, att := range allAtts {
		if !att.IsInline {
			regularAtts = append(regularAtts, att)
		}
	}
	if len(regularAtts) == 0 {
		return
	}

	// Fetch raw message from IMAP once for all attachments
	raw, fetchErr := a.syncEngine.FetchRawMessage(a.ctx, msg.AccountID, msg.FolderID, msg.UID)
	if fetchErr != nil {
		log.Warn().Err(fetchErr).Msg("Failed to fetch raw message for forward attachments (offline?)")
		return
	}

	downloader := email.NewAttachmentDownloader(a.paths.AttachmentsPath())
	for _, att := range regularAtts {
		content, extractErr := downloader.ExtractAttachmentContent(raw, att.Filename)
		if extractErr != nil {
			log.Warn().Err(extractErr).Str("filename", att.Filename).Msg("Failed to extract attachment for forward")
			continue
		}
		*attachments = append(*attachments, smtp.Attachment{
			Content:     content,
			ContentType: att.ContentType,
			Filename:    att.Filename,
			Inline:      false,
		})
	}
}

// parseDataURL extracts the content type and base64 data from a data URL.
// e.g., "data:image/png;base64,ABC123..." → ("image/png", "ABC123...")
func parseDataURL(dataURL string) (contentType, base64Data string) {
	// Strip "data:" prefix
	rest := strings.TrimPrefix(dataURL, "data:")
	// Split on ";base64,"
	idx := strings.Index(rest, ";base64,")
	if idx < 0 {
		return "", ""
	}
	return rest[:idx], rest[idx+8:]
}

// TestSMTPConnection tests SMTP connection settings
func (a *App) TestSMTPConnection(host string, port int, security, username, password string) error {
	log := logging.WithComponent("app")

	// Map security string to type
	var securityType smtp.SecurityType
	switch security {
	case "none":
		securityType = smtp.SecurityNone
	case "starttls":
		securityType = smtp.SecurityStartTLS
	case "tls":
		securityType = smtp.SecurityTLS
	default:
		securityType = smtp.SecurityStartTLS
	}

	// Create client config
	config := smtp.DefaultConfig()
	config.Host = host
	config.Port = port
	config.Security = securityType
	config.Username = username
	config.Password = password
	config.AuthType = smtp.AuthTypePassword
	config.TLSConfig = certificate.BuildTLSConfig(host, a.certStore)

	client := smtp.NewClient(config)

	// Test connection
	if err := client.Connect(); err != nil {
		log.Error().Err(err).Msg("SMTP connection test failed")
		return fmt.Errorf("connection failed: %w", err)
	}
	defer client.Close()

	// Test authentication
	if err := client.Login(); err != nil {
		log.Error().Err(err).Msg("SMTP login test failed")
		return fmt.Errorf("authentication failed: %w", err)
	}

	log.Info().Str("host", host).Int("port", port).Msg("SMTP connection test successful")
	return nil
}

// PickAttachmentFiles opens a file picker dialog and returns the selected files as attachments
func (a *App) PickAttachmentFiles() ([]ComposerAttachment, error) {
	return pickAttachmentFiles(a.ctx)
}

// ReadFileAsAttachment reads a file and creates a ComposerAttachment
func (a *App) ReadFileAsAttachment(filePath string) (*ComposerAttachment, error) {
	return readFileAsAttachment(filePath)
}

// ============================================================================
// Helper Functions
// ============================================================================

// parseAddressList parses a JSON array of addresses or comma-separated string
func parseAddressList(s string) []smtp.Address {
	if s == "" {
		return nil
	}

	// Try smtp.Address JSON format first (uses "address" field) —
	// this is what addressListToJSON stores for drafts
	var smtpAddrs []smtp.Address
	if err := json.Unmarshal([]byte(s), &smtpAddrs); err == nil {
		// Check if the addresses actually have data (not just zero values)
		if len(smtpAddrs) > 0 && smtpAddrs[0].Address != "" {
			return smtpAddrs
		}
	}

	// Try message.Address JSON format (uses "email" field) —
	// this is what the message store uses
	var msgAddrs []message.Address
	if err := json.Unmarshal([]byte(s), &msgAddrs); err == nil {
		var addrs []smtp.Address
		for _, msgAddr := range msgAddrs {
			addrs = append(addrs, smtp.Address{
				Name:    strings.TrimSpace(msgAddr.Name),
				Address: strings.TrimSpace(msgAddr.Email),
			})
		}
		if len(addrs) > 0 && addrs[0].Address != "" {
			return addrs
		}
	}

	// Try as comma-separated list (legacy format)
	var result []smtp.Address
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Parse "Name <email>" format
		if strings.Contains(part, "<") {
			start := strings.Index(part, "<")
			end := strings.Index(part, ">")
			if start > 0 && end > start {
				name := strings.TrimSpace(part[:start])
				email := strings.TrimSpace(part[start+1 : end])
				result = append(result, smtp.Address{Name: name, Address: email})
				continue
			}
		}
		result = append(result, smtp.Address{Address: strings.TrimSpace(part)})
	}
	return result
}

// filterSelfAddresses removes the user's own addresses from a list
func filterSelfAddresses(addrs []smtp.Address, selfEmails map[string]bool) []smtp.Address {
	var result []smtp.Address
	for _, addr := range addrs {
		lowerAddr := strings.ToLower(strings.TrimSpace(addr.Address))
		if !selfEmails[lowerAddr] {
			result = append(result, addr)
		}
	}
	return result
}

// escapeHTML escapes special HTML characters
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// ensureAngleBrackets wraps a Message-ID in angle brackets if not already present
func ensureAngleBrackets(msgID string) string {
	if msgID == "" {
		return ""
	}
	msgID = strings.TrimSpace(msgID)
	if !strings.HasPrefix(msgID, "<") {
		msgID = "<" + msgID
	}
	if !strings.HasSuffix(msgID, ">") {
		msgID = msgID + ">"
	}
	return msgID
}

// quoteText adds > prefix to each line for plain text quoting
func quoteText(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}

// providerAutoSavesSentMail checks if a mail provider automatically saves sent messages
func providerAutoSavesSentMail(host string) bool {
	host = strings.ToLower(host)
	autoSaveProviders := []string{
		"imap.gmail.com",       // Gmail
		"outlook.office365.com", // Microsoft 365
		"imap-mail.outlook.com", // Outlook.com
	}
	for _, provider := range autoSaveProviders {
		if strings.Contains(host, provider) {
			return true
		}
	}
	return false
}

// addressListToJSON converts an address list to JSON string
func addressListToJSON(addrs []smtp.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	data, _ := json.Marshal(addrs)
	return string(data)
}

// detectContentType returns the MIME type for a file based on extension
func detectContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	// Images
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".bmp":
		return "image/bmp"
	// Documents
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".odt":
		return "application/vnd.oasis.opendocument.text"
	case ".ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case ".odp":
		return "application/vnd.oasis.opendocument.presentation"
	// Text
	case ".txt":
		return "text/plain"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".csv":
		return "text/csv"
	case ".md":
		return "text/markdown"
	// Archives
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz", ".gzip":
		return "application/gzip"
	case ".7z":
		return "application/x-7z-compressed"
	case ".rar":
		return "application/vnd.rar"
	// Audio
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	// Video
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	default:
		return "application/octet-stream"
	}
}

