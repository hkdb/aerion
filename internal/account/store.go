package account

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hkdb/aerion/internal/database"
)

// Store provides account storage operations
type Store struct {
	db *database.DB
}

// Default colors for accounts (used for auto-assignment)
var defaultColors = []string{
	"#3B82F6", // blue
	"#10B981", // green
	"#F59E0B", // amber
	"#EF4444", // red
	"#8B5CF6", // purple
	"#EC4899", // pink
	"#06B6D4", // cyan
	"#F97316", // orange
}

// nullableString converts an empty string to nil for nullable database columns
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// getNextColor returns the next color in the rotation based on account count
func (s *Store) getNextColor() string {
	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count)
	return defaultColors[count%len(defaultColors)]
}

// NewStore creates a new account store
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// Create creates a new account
func (s *Store) Create(config *AccountConfig) (*Account, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// Check if account with this email already exists
	var exists bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM accounts WHERE email = ?)", config.Email).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing account: %w", err)
	}
	if exists {
		return nil, ErrAccountExists
	}

	// Get next order index
	var maxOrder int
	if err := s.db.QueryRow("SELECT COALESCE(MAX(order_index), -1) FROM accounts").Scan(&maxOrder); err != nil {
		return nil, fmt.Errorf("failed to get max account order: %w", err)
	}

	// Auto-assign color if not provided
	color := config.Color
	if color == "" {
		color = s.getNextColor()
	}

	now := time.Now()
	account := &Account{
		ID:                       uuid.New().String(),
		Name:                     config.Name,
		Email:                    config.Email,
		IMAPHost:                 config.IMAPHost,
		IMAPPort:                 config.IMAPPort,
		IMAPSecurity:             config.IMAPSecurity,
		SMTPHost:                 config.SMTPHost,
		SMTPPort:                 config.SMTPPort,
		SMTPSecurity:             config.SMTPSecurity,
		NoOutgoingServer:         config.NoOutgoingServer,
		SMTPUsername:             config.SMTPUsername,
		ReplyForwardIdentityID:   config.ReplyForwardIdentityID,
		AuthType:                 config.AuthType,
		Username:                 config.Username,
		Enabled:                  true,
		OrderIndex:               maxOrder + 1,
		Color:                    color,
		SyncPeriodDays:           config.SyncPeriodDays,
		SyncInterval:             config.SyncInterval,
		SyncAllFolders:           config.SyncAllFolders,
		SyncFoldersEnabled:       config.SyncFoldersEnabled,
		SharedMailboxParentID:    config.SharedMailboxParentID,
		ReadReceiptRequestPolicy: config.ReadReceiptRequestPolicy,
		SentFolderPath:           config.SentFolderPath,
		DraftsFolderPath:         config.DraftsFolderPath,
		TrashFolderPath:          config.TrashFolderPath,
		SpamFolderPath:           config.SpamFolderPath,
		ArchiveFolderPath:        config.ArchiveFolderPath,
		AllMailFolderPath:        config.AllMailFolderPath,
		StarredFolderPath:        config.StarredFolderPath,
		CreatedAt:                now,
		UpdatedAt:                now,
	}

	_, err = s.db.Exec(`
		INSERT INTO accounts (
			id, name, email, shared_mailbox_parent_id,
			imap_host, imap_port, imap_security,
			smtp_host, smtp_port, smtp_security,
			no_outgoing_server, smtp_username, reply_forward_identity_id,
			auth_type, username,
			enabled, order_index, color, sync_period_days, sync_interval, sync_all_folders, sync_folders_enabled,
			read_receipt_request_policy,
			sent_folder_path, drafts_folder_path, trash_folder_path,
			spam_folder_path, archive_folder_path, all_mail_folder_path,
			starred_folder_path,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		account.ID, account.Name, account.Email, nullableString(account.SharedMailboxParentID),
		account.IMAPHost, account.IMAPPort, account.IMAPSecurity,
		account.SMTPHost, account.SMTPPort, account.SMTPSecurity,
		boolToInt(account.NoOutgoingServer), account.SMTPUsername, account.ReplyForwardIdentityID,
		account.AuthType, account.Username,
		account.Enabled, account.OrderIndex, account.Color, account.SyncPeriodDays, account.SyncInterval, boolToInt(account.SyncAllFolders), boolToInt(account.SyncFoldersEnabled),
		account.ReadReceiptRequestPolicy,
		nullableString(account.SentFolderPath), nullableString(account.DraftsFolderPath), nullableString(account.TrashFolderPath),
		nullableString(account.SpamFolderPath), nullableString(account.ArchiveFolderPath), nullableString(account.AllMailFolderPath),
		nullableString(account.StarredFolderPath),
		account.CreatedAt, account.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert account: %w", err)
	}

	// Create default identity
	identity := &Identity{
		ID:         uuid.New().String(),
		AccountID:  account.ID,
		Email:      account.Email,
		Name:       config.DisplayName,
		IsDefault:  true,
		OrderIndex: 0,
		CreatedAt:  now,
	}

	_, err = s.db.Exec(`
		INSERT INTO identities (id, account_id, email, name, is_default, order_index, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, identity.ID, identity.AccountID, identity.Email, identity.Name, identity.IsDefault, identity.OrderIndex, identity.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create default identity: %w", err)
	}

	return account, nil
}

// Get retrieves an account by ID
func (s *Store) Get(id string) (*Account, error) {
	account := &Account{}
	var sentPath, draftsPath, trashPath, spamPath, archivePath, allMailPath, starredPath, sharedMailboxParentID sql.NullString
	var syncAllFolders, syncFoldersEnabled, noOutgoingServer int
	err := s.db.QueryRow(`
		SELECT id, name, email, shared_mailbox_parent_id,
			imap_host, imap_port, imap_security,
			smtp_host, smtp_port, smtp_security,
			no_outgoing_server, smtp_username, reply_forward_identity_id,
			auth_type, username,
			enabled, order_index, color, sync_period_days, sync_interval, sync_all_folders, sync_folders_enabled,
			read_receipt_request_policy,
			sent_folder_path, drafts_folder_path, trash_folder_path,
			spam_folder_path, archive_folder_path, all_mail_folder_path,
			starred_folder_path,
			created_at, updated_at
		FROM accounts WHERE id = ?
	`, id).Scan(
		&account.ID, &account.Name, &account.Email, &sharedMailboxParentID,
		&account.IMAPHost, &account.IMAPPort, &account.IMAPSecurity,
		&account.SMTPHost, &account.SMTPPort, &account.SMTPSecurity,
		&noOutgoingServer, &account.SMTPUsername, &account.ReplyForwardIdentityID,
		&account.AuthType, &account.Username,
		&account.Enabled, &account.OrderIndex, &account.Color, &account.SyncPeriodDays, &account.SyncInterval, &syncAllFolders, &syncFoldersEnabled,
		&account.ReadReceiptRequestPolicy,
		&sentPath, &draftsPath, &trashPath,
		&spamPath, &archivePath, &allMailPath,
		&starredPath,
		&account.CreatedAt, &account.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}
	account.SyncAllFolders = syncAllFolders == 1
	account.SyncFoldersEnabled = syncFoldersEnabled == 1
	account.NoOutgoingServer = noOutgoingServer == 1
	account.SharedMailboxParentID = sharedMailboxParentID.String
	// Map nullable strings to account fields
	account.SentFolderPath = sentPath.String
	account.DraftsFolderPath = draftsPath.String
	account.TrashFolderPath = trashPath.String
	account.SpamFolderPath = spamPath.String
	account.ArchiveFolderPath = archivePath.String
	account.AllMailFolderPath = allMailPath.String
	account.StarredFolderPath = starredPath.String
	return account, nil
}

// List retrieves all accounts ordered by order_index
func (s *Store) List() ([]*Account, error) {
	rows, err := s.db.Query(`
		SELECT id, name, email, shared_mailbox_parent_id,
			imap_host, imap_port, imap_security,
			smtp_host, smtp_port, smtp_security,
			no_outgoing_server, smtp_username, reply_forward_identity_id,
			auth_type, username,
			enabled, order_index, color, sync_period_days, sync_interval, sync_all_folders, sync_folders_enabled,
			read_receipt_request_policy,
			sent_folder_path, drafts_folder_path, trash_folder_path,
			spam_folder_path, archive_folder_path, all_mail_folder_path,
			starred_folder_path,
			created_at, updated_at
		FROM accounts ORDER BY order_index
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		account := &Account{}
		var sentPath, draftsPath, trashPath, spamPath, archivePath, allMailPath, starredPath, sharedMailboxParentID sql.NullString
		var syncAllFolders, syncFoldersEnabled, noOutgoingServer int
		err := rows.Scan(
			&account.ID, &account.Name, &account.Email, &sharedMailboxParentID,
			&account.IMAPHost, &account.IMAPPort, &account.IMAPSecurity,
			&account.SMTPHost, &account.SMTPPort, &account.SMTPSecurity,
			&noOutgoingServer, &account.SMTPUsername, &account.ReplyForwardIdentityID,
			&account.AuthType, &account.Username,
			&account.Enabled, &account.OrderIndex, &account.Color, &account.SyncPeriodDays, &account.SyncInterval, &syncAllFolders, &syncFoldersEnabled,
			&account.ReadReceiptRequestPolicy,
			&sentPath, &draftsPath, &trashPath,
			&spamPath, &archivePath, &allMailPath,
			&starredPath,
			&account.CreatedAt, &account.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan account: %w", err)
		}
		// Map nullable strings and booleans to account fields
		account.SyncAllFolders = syncAllFolders == 1
		account.SyncFoldersEnabled = syncFoldersEnabled == 1
		account.NoOutgoingServer = noOutgoingServer == 1
		account.SharedMailboxParentID = sharedMailboxParentID.String
		account.SentFolderPath = sentPath.String
		account.DraftsFolderPath = draftsPath.String
		account.TrashFolderPath = trashPath.String
		account.SpamFolderPath = spamPath.String
		account.ArchiveFolderPath = archivePath.String
		account.AllMailFolderPath = allMailPath.String
		account.StarredFolderPath = starredPath.String
		accounts = append(accounts, account)
	}

	return accounts, nil
}

// ListBySharedMailboxParent returns all shared mailbox accounts linked to a parent account.
func (s *Store) ListBySharedMailboxParent(parentID string) ([]*Account, error) {
	rows, err := s.db.Query(`
		SELECT id, name, email, shared_mailbox_parent_id,
			imap_host, imap_port, imap_security,
			smtp_host, smtp_port, smtp_security,
			no_outgoing_server, smtp_username, reply_forward_identity_id,
			auth_type, username,
			enabled, order_index, color, sync_period_days, sync_interval, sync_all_folders, sync_folders_enabled,
			read_receipt_request_policy,
			sent_folder_path, drafts_folder_path, trash_folder_path,
			spam_folder_path, archive_folder_path, all_mail_folder_path,
			starred_folder_path,
			created_at, updated_at
		FROM accounts WHERE shared_mailbox_parent_id = ? ORDER BY name
	`, parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to list shared mailboxes: %w", err)
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		account := &Account{}
		var sentPath, draftsPath, trashPath, spamPath, archivePath, allMailPath, starredPath, sharedMailboxParentID sql.NullString
		var syncAllFolders, syncFoldersEnabled, noOutgoingServer int
		err := rows.Scan(
			&account.ID, &account.Name, &account.Email, &sharedMailboxParentID,
			&account.IMAPHost, &account.IMAPPort, &account.IMAPSecurity,
			&account.SMTPHost, &account.SMTPPort, &account.SMTPSecurity,
			&noOutgoingServer, &account.SMTPUsername, &account.ReplyForwardIdentityID,
			&account.AuthType, &account.Username,
			&account.Enabled, &account.OrderIndex, &account.Color, &account.SyncPeriodDays, &account.SyncInterval, &syncAllFolders, &syncFoldersEnabled,
			&account.ReadReceiptRequestPolicy,
			&sentPath, &draftsPath, &trashPath,
			&spamPath, &archivePath, &allMailPath,
			&starredPath,
			&account.CreatedAt, &account.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan shared mailbox: %w", err)
		}
		account.SyncAllFolders = syncAllFolders == 1
		account.SyncFoldersEnabled = syncFoldersEnabled == 1
		account.NoOutgoingServer = noOutgoingServer == 1
		account.SharedMailboxParentID = sharedMailboxParentID.String
		account.SentFolderPath = sentPath.String
		account.DraftsFolderPath = draftsPath.String
		account.TrashFolderPath = trashPath.String
		account.SpamFolderPath = spamPath.String
		account.ArchiveFolderPath = archivePath.String
		account.AllMailFolderPath = allMailPath.String
		account.StarredFolderPath = starredPath.String
		accounts = append(accounts, account)
	}
	return accounts, nil
}

// Update updates an account
func (s *Store) Update(id string, config *AccountConfig) (*Account, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// Check if account exists
	existing, err := s.Get(id)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	_, err = s.db.Exec(`
		UPDATE accounts SET
			name = ?, email = ?,
			imap_host = ?, imap_port = ?, imap_security = ?,
			smtp_host = ?, smtp_port = ?, smtp_security = ?,
			no_outgoing_server = ?, smtp_username = ?, reply_forward_identity_id = ?,
			auth_type = ?, username = ?,
			color = ?, sync_period_days = ?, sync_interval = ?, sync_all_folders = ?, sync_folders_enabled = ?,
			read_receipt_request_policy = ?,
			sent_folder_path = ?, drafts_folder_path = ?, trash_folder_path = ?,
			spam_folder_path = ?, archive_folder_path = ?, all_mail_folder_path = ?,
			starred_folder_path = ?,
			updated_at = ?
		WHERE id = ?
	`,
		config.Name, config.Email,
		config.IMAPHost, config.IMAPPort, config.IMAPSecurity,
		config.SMTPHost, config.SMTPPort, config.SMTPSecurity,
		boolToInt(config.NoOutgoingServer), config.SMTPUsername, config.ReplyForwardIdentityID,
		config.AuthType, config.Username,
		config.Color, config.SyncPeriodDays, config.SyncInterval, boolToInt(config.SyncAllFolders), boolToInt(config.SyncFoldersEnabled),
		config.ReadReceiptRequestPolicy,
		nullableString(config.SentFolderPath), nullableString(config.DraftsFolderPath), nullableString(config.TrashFolderPath),
		nullableString(config.SpamFolderPath), nullableString(config.ArchiveFolderPath), nullableString(config.AllMailFolderPath),
		nullableString(config.StarredFolderPath),
		now, id,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update account: %w", err)
	}

	// Update the default identity's name (display name for sending)
	_, err = s.db.Exec(`
		UPDATE identities SET name = ? WHERE account_id = ? AND is_default = 1
	`, config.DisplayName, id)
	if err != nil {
		return nil, fmt.Errorf("failed to update default identity: %w", err)
	}

	existing.Name = config.Name
	existing.Email = config.Email
	existing.IMAPHost = config.IMAPHost
	existing.IMAPPort = config.IMAPPort
	existing.IMAPSecurity = config.IMAPSecurity
	existing.SMTPHost = config.SMTPHost
	existing.SMTPPort = config.SMTPPort
	existing.SMTPSecurity = config.SMTPSecurity
	existing.NoOutgoingServer = config.NoOutgoingServer
	existing.SMTPUsername = config.SMTPUsername
	existing.ReplyForwardIdentityID = config.ReplyForwardIdentityID
	existing.AuthType = config.AuthType
	existing.Username = config.Username
	existing.Color = config.Color
	existing.SyncPeriodDays = config.SyncPeriodDays
	existing.SyncInterval = config.SyncInterval
	existing.SyncAllFolders = config.SyncAllFolders
	existing.SyncFoldersEnabled = config.SyncFoldersEnabled
	existing.ReadReceiptRequestPolicy = config.ReadReceiptRequestPolicy
	existing.SentFolderPath = config.SentFolderPath
	existing.DraftsFolderPath = config.DraftsFolderPath
	existing.TrashFolderPath = config.TrashFolderPath
	existing.SpamFolderPath = config.SpamFolderPath
	existing.ArchiveFolderPath = config.ArchiveFolderPath
	existing.AllMailFolderPath = config.AllMailFolderPath
	existing.StarredFolderPath = config.StarredFolderPath
	existing.UpdatedAt = now

	return existing, nil
}

// UpdateFolderMappings updates only the folder path mappings for an account.
// Used to persist auto-detected folder mappings without requiring a full account update.
func (s *Store) UpdateFolderMappings(id, sent, drafts, trash, spam, archive, allMail, starred string) error {
	_, err := s.db.Exec(`
		UPDATE accounts SET
			sent_folder_path = ?, drafts_folder_path = ?, trash_folder_path = ?,
			spam_folder_path = ?, archive_folder_path = ?, all_mail_folder_path = ?,
			starred_folder_path = ?, updated_at = ?
		WHERE id = ?
	`,
		nullableString(sent), nullableString(drafts), nullableString(trash),
		nullableString(spam), nullableString(archive), nullableString(allMail),
		nullableString(starred), time.Now(), id,
	)
	if err != nil {
		return fmt.Errorf("failed to update folder mappings: %w", err)
	}
	return nil
}

// Delete deletes an account and all associated data
func (s *Store) Delete(id string) error {
	result, err := s.db.Exec("DELETE FROM accounts WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete account: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAccountNotFound
	}

	return nil
}

// SetEnabled enables or disables an account
func (s *Store) SetEnabled(id string, enabled bool) error {
	result, err := s.db.Exec("UPDATE accounts SET enabled = ?, updated_at = ? WHERE id = ?", enabled, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update account enabled status: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAccountNotFound
	}

	return nil
}

// Reorder updates the order of accounts
func (s *Store) Reorder(ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for i, id := range ids {
		_, err := tx.Exec("UPDATE accounts SET order_index = ? WHERE id = ?", i, id)
		if err != nil {
			return fmt.Errorf("failed to update order: %w", err)
		}
	}

	return tx.Commit()
}

// GetIdentities retrieves all identities for an account
func (s *Store) GetIdentities(accountID string) ([]*Identity, error) {
	rows, err := s.db.Query(`
		SELECT id, account_id, email, name, is_default, signature_html, signature_text,
			signature_enabled, signature_for_new, signature_for_reply, signature_for_forward,
			signature_placement, signature_separator, order_index, created_at, updated_at
		FROM identities WHERE account_id = ? ORDER BY order_index
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to list identities: %w", err)
	}
	defer rows.Close()

	var identities []*Identity
	for rows.Next() {
		identity := &Identity{}
		var sigHTML, sigText, placement sql.NullString
		var updatedAt sql.NullTime
		err := rows.Scan(
			&identity.ID, &identity.AccountID, &identity.Email, &identity.Name,
			&identity.IsDefault, &sigHTML, &sigText,
			&identity.SignatureEnabled, &identity.SignatureForNew, &identity.SignatureForReply, &identity.SignatureForForward,
			&placement, &identity.SignatureSeparator, &identity.OrderIndex, &identity.CreatedAt, &updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan identity: %w", err)
		}
		identity.SignatureHTML = sigHTML.String
		identity.SignatureText = sigText.String
		identity.SignaturePlacement = placement.String
		if identity.SignaturePlacement == "" {
			identity.SignaturePlacement = "above"
		}
		if updatedAt.Valid {
			identity.UpdatedAt = updatedAt.Time
		} else {
			identity.UpdatedAt = identity.CreatedAt
		}
		identities = append(identities, identity)
	}

	return identities, nil
}

// GetIdentity retrieves a single identity by ID
func (s *Store) GetIdentity(id string) (*Identity, error) {
	identity := &Identity{}
	var sigHTML, sigText, placement sql.NullString
	var updatedAt sql.NullTime
	err := s.db.QueryRow(`
		SELECT id, account_id, email, name, is_default, signature_html, signature_text,
			signature_enabled, signature_for_new, signature_for_reply, signature_for_forward,
			signature_placement, signature_separator, order_index, created_at, updated_at
		FROM identities WHERE id = ?
	`, id).Scan(
		&identity.ID, &identity.AccountID, &identity.Email, &identity.Name,
		&identity.IsDefault, &sigHTML, &sigText,
		&identity.SignatureEnabled, &identity.SignatureForNew, &identity.SignatureForReply, &identity.SignatureForForward,
		&placement, &identity.SignatureSeparator, &identity.OrderIndex, &identity.CreatedAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrIdentityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get identity: %w", err)
	}
	identity.SignatureHTML = sigHTML.String
	identity.SignatureText = sigText.String
	identity.SignaturePlacement = placement.String
	if identity.SignaturePlacement == "" {
		identity.SignaturePlacement = "above"
	}
	if updatedAt.Valid {
		identity.UpdatedAt = updatedAt.Time
	} else {
		identity.UpdatedAt = identity.CreatedAt
	}
	return identity, nil
}

// CreateIdentity creates a new identity for an account
func (s *Store) CreateIdentity(accountID string, config *IdentityConfig) (*Identity, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// Get next order index
	var maxOrder int
	if err := s.db.QueryRow("SELECT COALESCE(MAX(order_index), -1) FROM identities WHERE account_id = ?", accountID).Scan(&maxOrder); err != nil {
		return nil, fmt.Errorf("failed to get max identity order: %w", err)
	}

	now := time.Now()
	identity := &Identity{
		ID:                  uuid.New().String(),
		AccountID:           accountID,
		Email:               config.Email,
		Name:                config.Name,
		IsDefault:           false, // New identities are not default
		SignatureHTML:       config.SignatureHTML,
		SignatureText:       config.SignatureText,
		SignatureEnabled:    config.SignatureEnabled,
		SignatureForNew:     config.SignatureForNew,
		SignatureForReply:   config.SignatureForReply,
		SignatureForForward: config.SignatureForForward,
		SignaturePlacement:  config.SignaturePlacement,
		SignatureSeparator:  config.SignatureSeparator,
		OrderIndex:          maxOrder + 1,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	_, err := s.db.Exec(`
		INSERT INTO identities (
			id, account_id, email, name, is_default, signature_html, signature_text,
			signature_enabled, signature_for_new, signature_for_reply, signature_for_forward,
			signature_placement, signature_separator, order_index, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		identity.ID, identity.AccountID, identity.Email, identity.Name, identity.IsDefault,
		nullableString(identity.SignatureHTML), nullableString(identity.SignatureText),
		identity.SignatureEnabled, identity.SignatureForNew, identity.SignatureForReply, identity.SignatureForForward,
		identity.SignaturePlacement, identity.SignatureSeparator, identity.OrderIndex, identity.CreatedAt, identity.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create identity: %w", err)
	}

	return identity, nil
}

// UpdateIdentity updates an existing identity
func (s *Store) UpdateIdentity(id string, config *IdentityConfig) (*Identity, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// Check if identity exists
	existing, err := s.GetIdentity(id)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	_, err = s.db.Exec(`
		UPDATE identities SET
			email = ?, name = ?, signature_html = ?, signature_text = ?,
			signature_enabled = ?, signature_for_new = ?, signature_for_reply = ?, signature_for_forward = ?,
			signature_placement = ?, signature_separator = ?, updated_at = ?
		WHERE id = ?
	`,
		config.Email, config.Name, nullableString(config.SignatureHTML), nullableString(config.SignatureText),
		config.SignatureEnabled, config.SignatureForNew, config.SignatureForReply, config.SignatureForForward,
		config.SignaturePlacement, config.SignatureSeparator, now, id,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update identity: %w", err)
	}

	existing.Email = config.Email
	existing.Name = config.Name
	existing.SignatureHTML = config.SignatureHTML
	existing.SignatureText = config.SignatureText
	existing.SignatureEnabled = config.SignatureEnabled
	existing.SignatureForNew = config.SignatureForNew
	existing.SignatureForReply = config.SignatureForReply
	existing.SignatureForForward = config.SignatureForForward
	existing.SignaturePlacement = config.SignaturePlacement
	existing.SignatureSeparator = config.SignatureSeparator
	existing.UpdatedAt = now

	return existing, nil
}

// DeleteIdentity deletes an identity (cannot delete the default identity)
func (s *Store) DeleteIdentity(id string) error {
	// Check if identity exists and is not default
	identity, err := s.GetIdentity(id)
	if err != nil {
		return err
	}
	if identity.IsDefault {
		return ErrCannotDeleteDefaultIdentity
	}

	result, err := s.db.Exec("DELETE FROM identities WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete identity: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrIdentityNotFound
	}

	return nil
}

// SetDefaultIdentity sets an identity as the default for its account
func (s *Store) SetDefaultIdentity(accountID, identityID string) error {
	// Verify the identity exists and belongs to the account
	identity, err := s.GetIdentity(identityID)
	if err != nil {
		return err
	}
	if identity.AccountID != accountID {
		return ErrIdentityNotFound
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Unset current default
	_, err = tx.Exec("UPDATE identities SET is_default = 0 WHERE account_id = ?", accountID)
	if err != nil {
		return fmt.Errorf("failed to unset current default: %w", err)
	}

	// Set new default
	_, err = tx.Exec("UPDATE identities SET is_default = 1, updated_at = ? WHERE id = ?", time.Now(), identityID)
	if err != nil {
		return fmt.Errorf("failed to set new default: %w", err)
	}

	return tx.Commit()
}
