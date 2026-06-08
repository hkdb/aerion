package database

// Migration represents a database migration
type Migration struct {
	Version int
	SQL     string
}

// migrations is the list of all database migrations
var migrations = []Migration{
	{
		Version: 1,
		SQL: `
			-- Accounts table
			CREATE TABLE accounts (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				email TEXT NOT NULL UNIQUE,
				
				-- IMAP settings
				imap_host TEXT NOT NULL,
				imap_port INTEGER NOT NULL DEFAULT 993,
				imap_security TEXT NOT NULL DEFAULT 'tls',
				
				-- SMTP settings
				smtp_host TEXT NOT NULL,
				smtp_port INTEGER NOT NULL DEFAULT 587,
				smtp_security TEXT NOT NULL DEFAULT 'starttls',
				
				-- Authentication
				auth_type TEXT NOT NULL DEFAULT 'password',
				username TEXT NOT NULL,
				
				-- State
				enabled INTEGER NOT NULL DEFAULT 1,
				order_index INTEGER NOT NULL DEFAULT 0,
				
				-- Sync settings
				sync_period_days INTEGER NOT NULL DEFAULT 30,
				
				-- Timestamps
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			-- Sender identities (aliases)
			CREATE TABLE identities (
				id TEXT PRIMARY KEY,
				account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
				email TEXT NOT NULL,
				name TEXT NOT NULL,
				is_default INTEGER NOT NULL DEFAULT 0,
				signature_html TEXT,
				signature_text TEXT,
				order_index INTEGER NOT NULL DEFAULT 0,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			CREATE INDEX idx_identities_account ON identities(account_id);

			-- Folders table
			CREATE TABLE folders (
				id TEXT PRIMARY KEY,
				account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
				name TEXT NOT NULL,
				path TEXT NOT NULL,
				folder_type TEXT NOT NULL DEFAULT 'folder',
				parent_id TEXT REFERENCES folders(id) ON DELETE CASCADE,
				
				-- IMAP state
				uid_validity INTEGER,
				uid_next INTEGER,
				highest_mod_seq INTEGER,
				
				-- Counts
				total_count INTEGER DEFAULT 0,
				unread_count INTEGER DEFAULT 0,
				
				-- Sync state
				last_sync DATETIME,
				
				UNIQUE(account_id, path)
			);

			CREATE INDEX idx_folders_account ON folders(account_id);
			CREATE INDEX idx_folders_parent ON folders(parent_id);

			-- Messages table (envelope/header data)
			CREATE TABLE messages (
				id TEXT PRIMARY KEY,
				account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
				folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
				
				-- IMAP identifiers
				uid INTEGER NOT NULL,
				message_id TEXT,
				
				-- Threading
				in_reply_to TEXT,
				thread_id TEXT,
				
				-- Envelope data
				subject TEXT,
				from_name TEXT,
				from_email TEXT,
				to_list TEXT,
				cc_list TEXT,
				bcc_list TEXT,
				reply_to TEXT,
				date DATETIME,
				
				-- Preview
				snippet TEXT,
				
				-- Flags
				is_read INTEGER DEFAULT 0,
				is_starred INTEGER DEFAULT 0,
				is_answered INTEGER DEFAULT 0,
				is_forwarded INTEGER DEFAULT 0,
				is_draft INTEGER DEFAULT 0,
				is_deleted INTEGER DEFAULT 0,
				
				-- Size and attachments
				size INTEGER DEFAULT 0,
				has_attachments INTEGER DEFAULT 0,
				
				-- Body (stored separately for large messages)
				body_text TEXT,
				body_html TEXT,
				
				-- Timestamps
				received_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				
				UNIQUE(folder_id, uid)
			);

			CREATE INDEX idx_messages_account ON messages(account_id);
			CREATE INDEX idx_messages_folder ON messages(folder_id);
			CREATE INDEX idx_messages_date ON messages(date DESC);
			CREATE INDEX idx_messages_thread ON messages(thread_id);
			CREATE INDEX idx_messages_message_id ON messages(message_id);
			CREATE INDEX idx_messages_unread ON messages(folder_id, is_read) WHERE is_read = 0;

			-- Attachments table
			CREATE TABLE attachments (
				id TEXT PRIMARY KEY,
				message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
				filename TEXT NOT NULL,
				content_type TEXT,
				size INTEGER DEFAULT 0,
				content_id TEXT,
				is_inline INTEGER DEFAULT 0,
				local_path TEXT
			);

			CREATE INDEX idx_attachments_message ON attachments(message_id);

			-- Drafts table (local drafts before sync)
			CREATE TABLE drafts (
				id TEXT PRIMARY KEY,
				account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
				
				-- Composer state
				to_list TEXT,
				cc_list TEXT,
				bcc_list TEXT,
				subject TEXT,
				body_html TEXT,
				body_text TEXT,
				
				-- Reply context
				in_reply_to_id TEXT,
				reply_type TEXT,
				
				-- Identity
				identity_id TEXT REFERENCES identities(id),
				
				-- Timestamps
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			CREATE INDEX idx_drafts_account ON drafts(account_id);
		`,
	},
	{
		Version: 2,
		SQL: `
			-- Add encrypted password column for fallback credential storage
			-- Used when OS keyring is not available
			ALTER TABLE accounts ADD COLUMN encrypted_password TEXT;
		`,
	},
	{
		Version: 3,
		SQL: `
			-- Add references column for threading (stores References header as JSON array)
			ALTER TABLE messages ADD COLUMN references_list TEXT;
			
			-- Create index for faster thread lookups
			CREATE INDEX IF NOT EXISTS idx_messages_in_reply_to ON messages(in_reply_to);
		`,
	},
	{
		Version: 4,
		SQL: `
			-- Add sync-related fields to drafts table for local-first draft saving
			
			-- Sync status: pending, synced, failed
			ALTER TABLE drafts ADD COLUMN sync_status TEXT NOT NULL DEFAULT 'pending';
			
			-- IMAP UID when synced (null if not yet synced)
			ALTER TABLE drafts ADD COLUMN imap_uid INTEGER;
			
			-- Folder ID for the drafts folder
			ALTER TABLE drafts ADD COLUMN folder_id TEXT REFERENCES folders(id) ON DELETE SET NULL;
			
			-- References header for threading (JSON array)
			ALTER TABLE drafts ADD COLUMN references_list TEXT;
			
			-- Last sync attempt timestamp
			ALTER TABLE drafts ADD COLUMN last_sync_attempt DATETIME;
			
			-- Sync error message if failed
			ALTER TABLE drafts ADD COLUMN sync_error TEXT;
			
			-- Index for finding pending drafts to sync
			CREATE INDEX IF NOT EXISTS idx_drafts_sync_status ON drafts(sync_status);
		`,
	},
	{
		Version: 5,
		SQL: `
			-- Global settings table for application preferences
			CREATE TABLE IF NOT EXISTS settings (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL
			);
			
			-- Default read receipt response policy: 'never', 'ask', 'always'
			INSERT INTO settings (key, value) VALUES ('read_receipt_response_policy', 'ask');
			
			-- Per-account read receipt request policy
			-- Controls whether to request read receipts when sending emails
			-- Values: 'never' (default), 'ask', 'always'
			ALTER TABLE accounts ADD COLUMN read_receipt_request_policy TEXT NOT NULL DEFAULT 'never';
			
			-- Read receipt fields on messages
			-- read_receipt_to: Email address that requested the receipt (from Disposition-Notification-To header)
			ALTER TABLE messages ADD COLUMN read_receipt_to TEXT;
			
			-- read_receipt_handled: Whether the user has already responded (sent or ignored)
			ALTER TABLE messages ADD COLUMN read_receipt_handled INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		Version: 6,
		SQL: `
			-- Contact sources table (CardDAV servers/accounts)
			CREATE TABLE contact_sources (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				type TEXT NOT NULL,
				url TEXT NOT NULL,
				username TEXT,
				enabled INTEGER DEFAULT 1,
				sync_interval INTEGER DEFAULT 60,
				last_synced_at DATETIME,
				last_error TEXT,
				last_error_at DATETIME,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			-- Contact source addressbooks (which addressbooks to sync from each source)
			CREATE TABLE contact_source_addressbooks (
				id TEXT PRIMARY KEY,
				source_id TEXT NOT NULL,
				path TEXT NOT NULL,
				name TEXT,
				enabled INTEGER DEFAULT 1,
				sync_token TEXT,
				last_synced_at DATETIME,
				FOREIGN KEY (source_id) REFERENCES contact_sources(id) ON DELETE CASCADE
			);

			CREATE INDEX idx_contact_source_addressbooks_source ON contact_source_addressbooks(source_id);

			-- CardDAV contacts
			CREATE TABLE carddav_contacts (
				id TEXT PRIMARY KEY,
				addressbook_id TEXT NOT NULL,
				email TEXT NOT NULL,
				display_name TEXT,
				href TEXT,
				etag TEXT,
				synced_at DATETIME,
				FOREIGN KEY (addressbook_id) REFERENCES contact_source_addressbooks(id) ON DELETE CASCADE
			);

			CREATE INDEX idx_carddav_contacts_addressbook ON carddav_contacts(addressbook_id);
			CREATE INDEX idx_carddav_contacts_email ON carddav_contacts(email);
		`,
	},
	{
		Version: 7,
		SQL: `
			-- Add encrypted password column to contact_sources for fallback credential storage
			-- Used when OS keyring is not available
			ALTER TABLE contact_sources ADD COLUMN encrypted_password TEXT;
		`,
	},
	{
		Version: 8,
		SQL: `
			-- Add folder mapping columns to accounts table
			-- These allow users to override auto-detected special folders
			-- Empty/NULL means use auto-detection
			ALTER TABLE accounts ADD COLUMN sent_folder_path TEXT;
			ALTER TABLE accounts ADD COLUMN drafts_folder_path TEXT;
			ALTER TABLE accounts ADD COLUMN trash_folder_path TEXT;
			ALTER TABLE accounts ADD COLUMN spam_folder_path TEXT;
			ALTER TABLE accounts ADD COLUMN archive_folder_path TEXT;
			ALTER TABLE accounts ADD COLUMN all_mail_folder_path TEXT;
			ALTER TABLE accounts ADD COLUMN starred_folder_path TEXT;
		`,
	},
	{
		Version: 9,
		SQL: `
			-- OAuth token metadata table
			-- Sensitive tokens (access_token, refresh_token) are stored in OS keyring
			-- Only metadata (provider, expiry, scopes) is stored in DB
			-- Fallback encrypted columns are used when keyring is unavailable
			CREATE TABLE oauth_tokens (
				account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
				provider TEXT NOT NULL,  -- 'google', 'microsoft'
				expires_at DATETIME,
				scopes TEXT,  -- JSON array of granted scopes
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			-- Fallback encrypted token storage (when OS keyring is unavailable)
			ALTER TABLE accounts ADD COLUMN encrypted_access_token TEXT;
			ALTER TABLE accounts ADD COLUMN encrypted_refresh_token TEXT;
		`,
	},
	{
		Version: 10,
		SQL: `
			-- Incremental sync support: fetch headers first, bodies later
			-- Add body_fetched column to track whether full body has been downloaded
			-- Default to 1 (true) so existing messages are considered complete
			ALTER TABLE messages ADD COLUMN body_fetched INTEGER NOT NULL DEFAULT 1;

			-- Create index for efficient queries of messages without body
			-- Used during background body fetching
			CREATE INDEX IF NOT EXISTS idx_messages_body_fetched ON messages(folder_id, body_fetched);
		`,
	},
	{
		Version: 11,
		SQL: `
			-- Add sync_interval column to accounts for automatic email polling
			-- Default to 30 minutes. Value of 0 means manual sync only.
			-- This controls how often the app checks for new mail via polling.
			-- IMAP IDLE (push) is used when available for real-time notifications.
			ALTER TABLE accounts ADD COLUMN sync_interval INTEGER NOT NULL DEFAULT 30;
		`,
	},
	{
		Version: 12,
		SQL: `
			-- Add color column to accounts for visual identification in unified inbox
			-- Each account can have a unique color shown as a dot indicator
			ALTER TABLE accounts ADD COLUMN color TEXT NOT NULL DEFAULT '';
		`,
	},
	{
		Version: 13,
		SQL: `
			-- App state table for persisting UI state across sessions
			-- Uses a key-value design for flexibility in storing various state data
			CREATE TABLE IF NOT EXISTS app_state (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
		`,
	},
	{
		Version: 14,
		SQL: `
			-- Create FTS5 virtual table for full-text search
			-- Uses content= to create an "external content" table that shadows messages
			-- This avoids duplicating data while enabling fast full-text search
			CREATE VIRTUAL TABLE messages_fts USING fts5(
				subject,
				from_name,
				from_email,
				to_list,
				cc_list,
				snippet,
				body_text,
				content='messages',
				content_rowid='rowid'
			);

			-- Triggers to keep FTS in sync with messages table
			-- These fire on INSERT/UPDATE/DELETE to maintain index consistency
			
			CREATE TRIGGER messages_fts_insert AFTER INSERT ON messages BEGIN
				INSERT INTO messages_fts(rowid, subject, from_name, from_email, to_list, cc_list, snippet, body_text)
				VALUES (NEW.rowid, NEW.subject, NEW.from_name, NEW.from_email, NEW.to_list, NEW.cc_list, NEW.snippet, NEW.body_text);
			END;

			CREATE TRIGGER messages_fts_delete AFTER DELETE ON messages BEGIN
				INSERT INTO messages_fts(messages_fts, rowid, subject, from_name, from_email, to_list, cc_list, snippet, body_text)
				VALUES ('delete', OLD.rowid, OLD.subject, OLD.from_name, OLD.from_email, OLD.to_list, OLD.cc_list, OLD.snippet, OLD.body_text);
			END;

			CREATE TRIGGER messages_fts_update AFTER UPDATE ON messages BEGIN
				INSERT INTO messages_fts(messages_fts, rowid, subject, from_name, from_email, to_list, cc_list, snippet, body_text)
				VALUES ('delete', OLD.rowid, OLD.subject, OLD.from_name, OLD.from_email, OLD.to_list, OLD.cc_list, OLD.snippet, OLD.body_text);
				INSERT INTO messages_fts(rowid, subject, from_name, from_email, to_list, cc_list, snippet, body_text)
				VALUES (NEW.rowid, NEW.subject, NEW.from_name, NEW.from_email, NEW.to_list, NEW.cc_list, NEW.snippet, NEW.body_text);
			END;

			-- Track indexing status per folder for background indexing progress
			-- This allows the UI to show indexing progress and warn users if search
			-- results may be incomplete
			CREATE TABLE fts_index_status (
				folder_id TEXT PRIMARY KEY REFERENCES folders(id) ON DELETE CASCADE,
				indexed_count INTEGER DEFAULT 0,
				total_count INTEGER DEFAULT 0,
				is_complete INTEGER DEFAULT 0,
				last_indexed_at DATETIME
			);
		`,
	},
	{
		Version: 15,
		SQL: `
			-- Add signature settings to identities table
			-- These columns control signature behavior per identity

			-- Master toggle for signature (default: enabled)
			ALTER TABLE identities ADD COLUMN signature_enabled INTEGER NOT NULL DEFAULT 1;

			-- When to append signature (default: all enabled)
			ALTER TABLE identities ADD COLUMN signature_for_new INTEGER NOT NULL DEFAULT 1;
			ALTER TABLE identities ADD COLUMN signature_for_reply INTEGER NOT NULL DEFAULT 1;
			ALTER TABLE identities ADD COLUMN signature_for_forward INTEGER NOT NULL DEFAULT 1;

			-- Signature placement in replies/forwards: 'above' or 'below' quoted text
			ALTER TABLE identities ADD COLUMN signature_placement TEXT NOT NULL DEFAULT 'above';

			-- Whether to add "-- " separator before signature (default: off)
			ALTER TABLE identities ADD COLUMN signature_separator INTEGER NOT NULL DEFAULT 0;

			-- Updated timestamp for identities (NULL default, set by application code)
			ALTER TABLE identities ADD COLUMN updated_at DATETIME;
		`,
	},
	{
		Version: 16,
		SQL: `
			-- Image allowlist table for "Always Load" remote images feature
			-- Allows users to trust specific senders or domains to auto-load images
			-- type: 'domain' (e.g., 'company.com') or 'sender' (e.g., 'newsletter@company.com')
			CREATE TABLE IF NOT EXISTS image_allowlist (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				type TEXT NOT NULL CHECK(type IN ('domain', 'sender')),
				value TEXT NOT NULL COLLATE NOCASE,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				UNIQUE(type, value)
			);

			CREATE INDEX idx_image_allowlist_type_value ON image_allowlist(type, value);
		`,
	},
	{
		Version: 17,
		SQL: `
			-- Add account_id to contact_sources for linking OAuth contact sources to email accounts
			-- NULL = standalone OAuth source, non-NULL = linked to email account's OAuth tokens
			ALTER TABLE contact_sources ADD COLUMN account_id TEXT REFERENCES accounts(id) ON DELETE CASCADE;

			-- OAuth token metadata for standalone contact sources (not linked to email accounts)
			-- Actual tokens stored in OS keyring, fallback to encrypted columns in contact_sources
			CREATE TABLE contact_source_oauth (
				source_id TEXT PRIMARY KEY REFERENCES contact_sources(id) ON DELETE CASCADE,
				provider TEXT NOT NULL,  -- 'google', 'microsoft'
				expires_at DATETIME,
				scopes TEXT,  -- JSON array of granted scopes
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			-- Fallback encrypted token storage for standalone contact sources
			ALTER TABLE contact_sources ADD COLUMN encrypted_access_token TEXT;
			ALTER TABLE contact_sources ADD COLUMN encrypted_refresh_token TEXT;

			-- Index for finding linked contact sources by email account
			CREATE INDEX idx_contact_sources_account ON contact_sources(account_id);
		`,
	},
	{
		Version: 18,
		SQL: `
			-- Trusted certificates table for certificate trust-on-first-use (TOFU)
			-- Trust is checked by fingerprint (global). Host is stored for UI filtering.
			CREATE TABLE IF NOT EXISTS trusted_certificates (
				id TEXT PRIMARY KEY,
				fingerprint TEXT NOT NULL UNIQUE,
				host TEXT NOT NULL DEFAULT '',
				subject TEXT NOT NULL,
				issuer TEXT NOT NULL,
				not_before DATETIME,
				not_after DATETIME,
				accepted_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
		`,
	},
	{
		Version: 19,
		SQL: `
			-- S/MIME user certificates (imported PKCS#12 with private key in keyring/encrypted fallback)
			CREATE TABLE IF NOT EXISTS smime_certificates (
				id TEXT PRIMARY KEY,
				account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
				email TEXT NOT NULL,
				subject TEXT NOT NULL,
				issuer TEXT NOT NULL,
				serial_number TEXT NOT NULL,
				fingerprint TEXT NOT NULL UNIQUE,
				not_before DATETIME NOT NULL,
				not_after DATETIME NOT NULL,
				cert_chain_pem TEXT NOT NULL,
				encrypted_private_key TEXT,
				is_default INTEGER NOT NULL DEFAULT 0,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			CREATE INDEX IF NOT EXISTS idx_smime_certificates_account ON smime_certificates(account_id);
			CREATE INDEX IF NOT EXISTS idx_smime_certificates_email ON smime_certificates(email);

			-- Auto-collected sender public certificates (from incoming signed messages)
			CREATE TABLE IF NOT EXISTS smime_sender_certs (
				id TEXT PRIMARY KEY,
				email TEXT NOT NULL,
				subject TEXT NOT NULL,
				issuer TEXT NOT NULL,
				serial_number TEXT NOT NULL,
				fingerprint TEXT NOT NULL UNIQUE,
				not_before DATETIME NOT NULL,
				not_after DATETIME NOT NULL,
				cert_pem TEXT NOT NULL,
				collected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			CREATE INDEX IF NOT EXISTS idx_smime_sender_certs_email ON smime_sender_certs(email);
			CREATE INDEX IF NOT EXISTS idx_smime_sender_certs_fingerprint ON smime_sender_certs(fingerprint);

			-- Cached verification results on messages
			ALTER TABLE messages ADD COLUMN smime_status TEXT;
			ALTER TABLE messages ADD COLUMN smime_signer_email TEXT;
			ALTER TABLE messages ADD COLUMN smime_signer_subject TEXT;

			-- Per-account signing policy
			ALTER TABLE accounts ADD COLUMN smime_sign_policy TEXT NOT NULL DEFAULT 'never';
			ALTER TABLE accounts ADD COLUMN smime_default_cert_id TEXT;
		`,
	},
	{
		Version: 20,
		SQL: `
			-- Raw S/MIME body for on-view verification/decryption
			ALTER TABLE messages ADD COLUMN smime_raw_body BLOB;

			-- Whether the message is encrypted (so viewer knows to decrypt)
			ALTER TABLE messages ADD COLUMN smime_encrypted INTEGER NOT NULL DEFAULT 0;

			-- Per-account encryption policy
			ALTER TABLE accounts ADD COLUMN smime_encrypt_policy TEXT NOT NULL DEFAULT 'never';
		`,
	},
	{
		Version: 21,
		SQL: `
			-- Whether the draft body is encrypted (encrypt-to-self)
			ALTER TABLE drafts ADD COLUMN encrypted INTEGER NOT NULL DEFAULT 0;

			-- Encrypted draft body (PKCS#7 DER blob)
			ALTER TABLE drafts ADD COLUMN encrypted_body BLOB;
		`,
	},
	{
		Version: 22,
		SQL: `
			-- Per-message S/MIME sign preference (preserved across draft save/load)
			ALTER TABLE drafts ADD COLUMN sign_message INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		Version: 23,
		SQL: `
			-- Store attachment data alongside draft body (inline images + regular attachments)
			-- JSON-serialized []smtp.Attachment for non-encrypted drafts
			-- For encrypted drafts, attachments are included in the encrypted_body payload
			ALTER TABLE drafts ADD COLUMN attachments_data BLOB;
		`,
	},
	{
		Version: 24,
		SQL: `
			-- PGP user keypairs
			CREATE TABLE IF NOT EXISTS pgp_keys (
				id TEXT PRIMARY KEY,
				account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
				email TEXT NOT NULL,
				key_id TEXT NOT NULL,
				fingerprint TEXT NOT NULL UNIQUE,
				user_id TEXT NOT NULL,
				algorithm TEXT NOT NULL,
				key_size INTEGER,
				created_at_key DATETIME,
				expires_at_key DATETIME,
				public_key_armored TEXT NOT NULL,
				encrypted_private_key TEXT,
				is_default INTEGER NOT NULL DEFAULT 0,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			CREATE INDEX IF NOT EXISTS idx_pgp_keys_account ON pgp_keys(account_id);
			CREATE INDEX IF NOT EXISTS idx_pgp_keys_email ON pgp_keys(email);
			CREATE INDEX IF NOT EXISTS idx_pgp_keys_fingerprint ON pgp_keys(fingerprint);

			-- Collected sender public keys
			CREATE TABLE IF NOT EXISTS pgp_sender_keys (
				id TEXT PRIMARY KEY,
				email TEXT NOT NULL,
				key_id TEXT NOT NULL,
				fingerprint TEXT NOT NULL UNIQUE,
				user_id TEXT NOT NULL,
				algorithm TEXT NOT NULL,
				key_size INTEGER,
				created_at_key DATETIME,
				expires_at_key DATETIME,
				public_key_armored TEXT NOT NULL,
				source TEXT NOT NULL DEFAULT 'message',
				collected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			CREATE INDEX IF NOT EXISTS idx_pgp_sender_keys_email ON pgp_sender_keys(email);
			CREATE INDEX IF NOT EXISTS idx_pgp_sender_keys_fingerprint ON pgp_sender_keys(fingerprint);

			-- Message PGP columns (parallel to smime_* columns)
			ALTER TABLE messages ADD COLUMN pgp_status TEXT;
			ALTER TABLE messages ADD COLUMN pgp_signer_email TEXT;
			ALTER TABLE messages ADD COLUMN pgp_signer_key_id TEXT;
			ALTER TABLE messages ADD COLUMN pgp_raw_body BLOB;
			ALTER TABLE messages ADD COLUMN pgp_encrypted INTEGER NOT NULL DEFAULT 0;

			-- Account PGP policies
			ALTER TABLE accounts ADD COLUMN pgp_sign_policy TEXT NOT NULL DEFAULT 'never';
			ALTER TABLE accounts ADD COLUMN pgp_encrypt_policy TEXT NOT NULL DEFAULT 'never';
			ALTER TABLE accounts ADD COLUMN pgp_default_key_id TEXT;

			-- Draft PGP fields
			ALTER TABLE drafts ADD COLUMN pgp_sign_message INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE drafts ADD COLUMN pgp_encrypted INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE drafts ADD COLUMN pgp_encrypted_body BLOB;
		`,
	},
	{
		Version: 25,
		SQL: `
			-- PGP key servers table (user-manageable, including defaults)
			CREATE TABLE IF NOT EXISTS pgp_keyservers (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				url TEXT NOT NULL UNIQUE,
				order_index INTEGER NOT NULL DEFAULT 0,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			INSERT OR IGNORE INTO pgp_keyservers (url, order_index) VALUES
				('https://keys.openpgp.org', 0),
				('https://keyserver.ubuntu.com', 1),
				('https://pgp.mit.edu', 2);
		`,
	},
	{
		Version: 26,
		SQL: `
			ALTER TABLE accounts ADD COLUMN sync_all_folders INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE folders ADD COLUMN subscribed INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		Version: 27,
		SQL:     `ALTER TABLE accounts ADD COLUMN sync_folders_enabled INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		Version: 28,
		SQL:     `ALTER TABLE accounts ADD COLUMN shared_mailbox_parent_id TEXT DEFAULT NULL;`,
	},
	{
		Version: 29,
		SQL: `
			-- Extension system Phase 1: multi-config OAuth support.
			--
			-- Each extension owns its own OAuth client configuration (Google Cloud
			-- project / Azure AD app registration). The same account can now have
			-- separate token rows under different client_config_ids — Mail tokens
			-- under 'google-mail', Calendar tokens under 'google-extensions', etc.
			--
			-- For backward compatibility: existing rows are backfilled to
			-- 'google-mail' / 'microsoft-mail' so all current accounts keep working
			-- with no user-visible change.

			-- Step 1: Add column to oauth_tokens and backfill.
			ALTER TABLE oauth_tokens ADD COLUMN client_config_id TEXT;
			UPDATE oauth_tokens SET client_config_id = 'google-mail'    WHERE provider = 'google'    AND client_config_id IS NULL;
			UPDATE oauth_tokens SET client_config_id = 'microsoft-mail' WHERE provider = 'microsoft' AND client_config_id IS NULL;

			-- Step 2: Change PK from (account_id) to (account_id, client_config_id)
			-- via the SQLite swap-table dance (ALTER TABLE can't change PK in place).
			CREATE TABLE oauth_tokens_new (
				account_id       TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
				client_config_id TEXT NOT NULL,
				provider         TEXT NOT NULL,
				expires_at       DATETIME,
				scopes           TEXT,
				created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (account_id, client_config_id)
			);
			INSERT INTO oauth_tokens_new (account_id, client_config_id, provider, expires_at, scopes, created_at, updated_at)
				SELECT account_id, client_config_id, provider, expires_at, scopes, created_at, updated_at
				FROM oauth_tokens;
			DROP TABLE oauth_tokens;
			ALTER TABLE oauth_tokens_new RENAME TO oauth_tokens;

			-- Step 3: Add the same column to contact_source_oauth for routing parity.
			-- PK stays as source_id (one source has one set of tokens).
			ALTER TABLE contact_source_oauth ADD COLUMN client_config_id TEXT;
			UPDATE contact_source_oauth SET client_config_id = 'google-mail'    WHERE provider = 'google'    AND client_config_id IS NULL;
			UPDATE contact_source_oauth SET client_config_id = 'microsoft-mail' WHERE provider = 'microsoft' AND client_config_id IS NULL;
		`,
	},
	{
		Version: 30,
		SQL: `
			-- Phase 2b: write capability flag for contact sources.
			--
			-- contact_sources.writable: explicit per-source write capability flag.
			-- CardDAV sources flip this on when the user opts in (no consent needed,
			-- credentials already cover both directions). OAuth sources flip this on
			-- only after the user completes incremental consent for write scopes
			-- under the per-extension client_config_id (e.g., google-contacts).
			--
			-- Note: contacts.name_overridden is added by contact.Store.ensureTable
			-- (lazy schema) since the contacts table isn't part of the migration
			-- system — see internal/contact/store.go.

			ALTER TABLE contact_sources ADD COLUMN writable INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		Version: 31,
		SQL: `
			-- Phase 2b.2.a: Unified contact-record schema.
			--
			-- This migration replaces the legacy denormalized "contacts" (autocomplete-
			-- by-email-only) and "carddav_contacts" (per-email fan-out) tables with a
			-- single unified record-based shape:
			--
			--   contact_records      → one row per logical contact (local or carddav)
			--   contact_emails       → composite-PK (record_id, email) with per-email
			--                          autocomplete metadata (send_count, last_used,
			--                          name_overridden). Replaces the legacy contacts
			--                          table as the autocomplete index.
			--   contact_phones       → multi-value per record
			--   contact_addresses    → multi-value per record (structured parts)
			--   contact_urls         → multi-value per record
			--   contact_impps        → multi-value per record (instant messaging)
			--   contact_categories   → multi-value per record (tags)
			--   carddav_record_state → CardDAV-only sidecar: href + etag + synced_at
			--                          + addressbook_id. ON DELETE CASCADE so removing
			--                          a record removes its sync state.
			--
			-- This migration is the architectural pivot for the Contacts extension:
			-- - Mail's autocomplete still works through contact.Store's public API
			--   (Search/AddOrUpdate/Get) — only the internals change to query the
			--   unified tables.
			-- - Multi-field reads land (phone/address/org/etc.) — vCard parser
			--   expanded to extract them in the same release.
			-- - One-row-per-vCard semantics for CardDAV (fixes the duplicate-row UX
			--   wart where a 2-email vCard appeared twice in the list).
			--
			-- Downgrade: tools/db/rollback-v31.sql reconstructs the legacy tables from
			-- this unified schema via JOIN — no separate backup file required.
			-- Multi-field data is inherently lost on rollback (v30 schema has no
			-- columns for it). Documented in docs/SQL_ROLLBACK.md.

			-- Defensive: contact.Store.ensureTable creates "contacts" lazily AFTER
			-- migrations run, so on a fresh install the table won't exist here. Make
			-- sure it exists so the backfill SELECTs below don't error.
			--
			-- Shape matches the LEGACY (pre-v0.3.0) ensureTable schema — no
			-- kind, no name_overridden. Older Aerion installs (≤ v0.2.4) have
			-- the table in this shape, so referencing those columns in the
			-- backfill SELECTs would fail on real production DBs. Defaults
			-- for the post-migration columns are supplied as literals in the
			-- INSERTs below.
			CREATE TABLE IF NOT EXISTS contacts (
				email TEXT PRIMARY KEY,
				display_name TEXT,
				send_count INTEGER DEFAULT 0,
				last_used DATETIME,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			-- New tables.

			CREATE TABLE contact_records (
				id            TEXT PRIMARY KEY,
				source        TEXT NOT NULL,             -- 'local' | 'carddav' (future: 'google' | 'microsoft')
				kind          TEXT,                      -- local: 'manual' | 'collected'; NULL for carddav
				source_ref    TEXT,                      -- carddav: addressbook_id. local: NULL
				fn            TEXT,                      -- vCard FN (display name)
				n_given       TEXT,                      -- vCard N: given name
				n_family      TEXT,                      -- vCard N: family name
				org           TEXT,
				title         TEXT,
				note          TEXT,
				bday          TEXT,                      -- ISO-8601 date string (vCard BDAY)
				nickname      TEXT,
				vcard_raw     TEXT,                      -- Preserved original vCard for unknown-property round-trip; NULL for local
				created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			CREATE INDEX idx_contact_records_source ON contact_records(source);
			CREATE INDEX idx_contact_records_source_kind ON contact_records(source, kind);
			CREATE INDEX idx_contact_records_source_ref ON contact_records(source_ref);

			CREATE TABLE contact_emails (
				record_id       TEXT NOT NULL REFERENCES contact_records(id) ON DELETE CASCADE,
				email           TEXT NOT NULL,                  -- normalized lowercase
				email_type      TEXT,                           -- vCard TYPE param: 'home', 'work', 'internet', etc.
				is_primary      INTEGER NOT NULL DEFAULT 0,
				send_count      INTEGER NOT NULL DEFAULT 0,     -- per-email autocomplete ranking
				last_used       DATETIME,
				name_overridden INTEGER NOT NULL DEFAULT 0,     -- preserves user-edited fn across auto-collection
				PRIMARY KEY (record_id, email)
			);

			CREATE INDEX idx_contact_emails_email ON contact_emails(email);
			CREATE INDEX idx_contact_emails_rank ON contact_emails(send_count DESC, last_used DESC);

			CREATE TABLE contact_phones (
				record_id   TEXT NOT NULL REFERENCES contact_records(id) ON DELETE CASCADE,
				number      TEXT NOT NULL,
				phone_type  TEXT,
				is_primary  INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (record_id, number)
			);

			CREATE TABLE contact_addresses (
				record_id   TEXT NOT NULL REFERENCES contact_records(id) ON DELETE CASCADE,
				addr_type   TEXT,                       -- 'home', 'work', etc.
				street      TEXT,
				city        TEXT,
				region      TEXT,
				postcode    TEXT,
				country     TEXT,
				-- No natural PK; allow duplicates and let app sort it out
				idx         INTEGER NOT NULL DEFAULT 0  -- ordinal for stable display order
			);

			CREATE INDEX idx_contact_addresses_record ON contact_addresses(record_id);

			CREATE TABLE contact_urls (
				record_id   TEXT NOT NULL REFERENCES contact_records(id) ON DELETE CASCADE,
				url         TEXT NOT NULL,
				url_type    TEXT,
				PRIMARY KEY (record_id, url)
			);

			CREATE TABLE contact_impps (
				record_id   TEXT NOT NULL REFERENCES contact_records(id) ON DELETE CASCADE,
				handle      TEXT NOT NULL,            -- e.g. xmpp:user@host
				impp_type   TEXT,
				PRIMARY KEY (record_id, handle)
			);

			CREATE TABLE contact_categories (
				record_id   TEXT NOT NULL REFERENCES contact_records(id) ON DELETE CASCADE,
				category    TEXT NOT NULL,
				PRIMARY KEY (record_id, category)
			);

			CREATE TABLE carddav_record_state (
				record_id       TEXT PRIMARY KEY REFERENCES contact_records(id) ON DELETE CASCADE,
				addressbook_id  TEXT NOT NULL,
				href            TEXT NOT NULL UNIQUE,
				etag            TEXT,
				synced_at       DATETIME
			);

			CREATE INDEX idx_carddav_record_state_addressbook ON carddav_record_state(addressbook_id);

			-- Backfill from legacy contacts. One record per row (email is the natural
			-- record-grain for local contacts today; multi-field expansion for local
			-- happens via the new sub-tables which start empty).
			-- record id: derived from email so subsequent linking via record_id is
			-- stable. Older Aerion never exposed contact ids externally; this just
			-- needs to be unique + deterministic within the migration.
			-- kind / name_overridden are hardcoded literals here rather than
			-- selected from the contacts table because legacy v0.2.x DBs
			-- don't have those columns (the legacy ensureTable shape never
			-- included them). Semantic match: legacy local contacts were
			-- exclusively auto-collected from sent mail (no manual-add UI
			-- before v0.3.0), and name_overridden was never set, so
			-- 'collected' / 0 are the correct historical defaults.
			INSERT INTO contact_records (id, source, kind, fn, created_at, updated_at)
			SELECT
				'local-' || email,
				'local',
				'collected',
				display_name,
				created_at,
				created_at
			FROM contacts;

			INSERT INTO contact_emails (record_id, email, send_count, last_used, name_overridden, is_primary)
			SELECT
				'local-' || email,
				email,
				send_count,
				last_used,
				0,
				1
			FROM contacts;

			-- Backfill from legacy carddav_contacts. Consolidate fan-out: group by
			-- (addressbook_id, href) so one record represents one vCard regardless
			-- of how many email rows the old schema fanned it into. The MIN(id)
			-- picks an arbitrary-but-deterministic representative id to reuse.
			INSERT INTO contact_records (id, source, source_ref, fn, created_at, updated_at)
			SELECT
				MIN(id),
				'carddav',
				addressbook_id,
				MIN(display_name),  -- first display_name encountered for the href
				MIN(synced_at),
				MAX(synced_at)
			FROM carddav_contacts
			WHERE href IS NOT NULL AND href != ''
			GROUP BY addressbook_id, href;

			-- Temp index so the canonical-group JOIN below isn't O(N²) on large
			-- addressbooks. carddav_contacts has indexes on addressbook_id and
			-- email but not (addressbook_id, href). Without this, the next
			-- INSERT can take minutes on a 5k-contact source. Index goes away
			-- when we DROP carddav_contacts at the end of the migration.
			CREATE INDEX IF NOT EXISTS idx_carddav_contacts_ab_href_tmp
				ON carddav_contacts(addressbook_id, href);

			-- All emails from each fanned-out group attach to the same record_id
			-- (the MIN(id) chosen above). Pre-compute the (addressbook_id, href)
			-- → representative-id mapping ONCE in a subquery (canonical), then
			-- JOIN every email row against it. Replaces a per-row correlated
			-- subquery that was O(N²) — fast on small sets, but locks the app
			-- for minutes on real addressbooks.
			INSERT INTO contact_emails (record_id, email, send_count, name_overridden, is_primary)
			SELECT
				canonical.rec_id,
				cc.email,
				0,
				0,
				CASE WHEN cc.id = canonical.rec_id THEN 1 ELSE 0 END
			FROM carddav_contacts cc
			JOIN (
				SELECT MIN(id) AS rec_id, addressbook_id, href
				FROM carddav_contacts
				WHERE href IS NOT NULL AND href != ''
				GROUP BY addressbook_id, href
			) AS canonical
			  ON canonical.addressbook_id = cc.addressbook_id
			 AND canonical.href = cc.href
			WHERE cc.href IS NOT NULL AND cc.href != '';

			-- Sidecar state: one row per consolidated record.
			INSERT INTO carddav_record_state (record_id, addressbook_id, href, etag, synced_at)
			SELECT
				cr.id,
				cr.source_ref,
				cc.href,
				cc.etag,
				cc.synced_at
			FROM contact_records cr
			JOIN carddav_contacts cc ON cc.id = cr.id
			WHERE cr.source = 'carddav';

			-- Drop legacy tables — the unified schema is now authoritative.
			DROP TABLE contacts;
			DROP TABLE carddav_contacts;
		`,
	},
	{
		Version: 32,
		SQL: `
			-- Phase 2b.2 follow-up: rewrite local contact_records IDs from the
			-- synthetic "local-<email>" form into real UUIDv4s.
			--
			-- Why: vCard/CardDAV identity is the UID (RFC 6350 §6.7.6) — one
			-- UID, multiple EMAILs, EMAILs are fully editable. CardDAV records
			-- already follow this (UUID + multi-email sub-rows). Local records
			-- got the "local-<email>" shape in migration 31 as a leftover from
			-- the legacy contacts(email PK) schema. That blocks email editing
			-- and creates a record-per-email asymmetry between local and CardDAV.
			--
			-- This migration unifies the identity shape: every contact_records
			-- row gets a UUID, and the EMAIL becomes a fully-editable sub-row in
			-- contact_emails. The multi-field Edit/Create UIs landing in 2b.2.b
			-- and 2b.2.c then design once for both sources.
			--
			-- Implementation: SQLite's randomblob() generates a fresh value per
			-- call, so each row gets its own UUID. PRAGMA defer_foreign_keys
			-- lets us update contact_records.id and the dependent
			-- contact_emails.record_id (plus sub-tables) without intermediate
			-- FK violations — the check is deferred to commit time.

			PRAGMA defer_foreign_keys = ON;

			-- Build the old-id → new-uuid mapping. One row per source='local'
			-- record. The UUID format is canonical 8-4-4-4-12 hex with version
			-- nibble 4 and variant nibble 8/9/a/b — matches RFC 4122 UUIDv4.
			CREATE TEMPORARY TABLE _migration_32_idmap (
				old_id TEXT PRIMARY KEY,
				new_id TEXT NOT NULL UNIQUE
			);

			INSERT INTO _migration_32_idmap (old_id, new_id)
			SELECT
				id,
				lower(
					substr(hex(randomblob(4)), 1, 8) || '-' ||
					substr(hex(randomblob(2)), 1, 4) || '-4' ||
					substr(hex(randomblob(2)), 2, 3) || '-' ||
					substr('89ab', 1 + (abs(random()) % 4), 1) ||
					substr(hex(randomblob(2)), 2, 3) || '-' ||
					substr(hex(randomblob(6)), 1, 12)
				)
			FROM contact_records
			WHERE source = 'local';

			-- Apply the new IDs to contact_records and every sub-table that
			-- references record_id. carddav_record_state is excluded — its
			-- record_id always points at source='carddav' records, never local.
			UPDATE contact_records
			SET id = (SELECT new_id FROM _migration_32_idmap WHERE old_id = contact_records.id)
			WHERE id IN (SELECT old_id FROM _migration_32_idmap);

			UPDATE contact_emails
			SET record_id = (SELECT new_id FROM _migration_32_idmap WHERE old_id = contact_emails.record_id)
			WHERE record_id IN (SELECT old_id FROM _migration_32_idmap);

			UPDATE contact_phones
			SET record_id = (SELECT new_id FROM _migration_32_idmap WHERE old_id = contact_phones.record_id)
			WHERE record_id IN (SELECT old_id FROM _migration_32_idmap);

			UPDATE contact_addresses
			SET record_id = (SELECT new_id FROM _migration_32_idmap WHERE old_id = contact_addresses.record_id)
			WHERE record_id IN (SELECT old_id FROM _migration_32_idmap);

			UPDATE contact_urls
			SET record_id = (SELECT new_id FROM _migration_32_idmap WHERE old_id = contact_urls.record_id)
			WHERE record_id IN (SELECT old_id FROM _migration_32_idmap);

			UPDATE contact_impps
			SET record_id = (SELECT new_id FROM _migration_32_idmap WHERE old_id = contact_impps.record_id)
			WHERE record_id IN (SELECT old_id FROM _migration_32_idmap);

			UPDATE contact_categories
			SET record_id = (SELECT new_id FROM _migration_32_idmap WHERE old_id = contact_categories.record_id)
			WHERE record_id IN (SELECT old_id FROM _migration_32_idmap);

			DROP TABLE _migration_32_idmap;
		`,
	},
	{
		Version: 33,
		SQL: `
			-- Phase 2b.2.b.1 follow-up: add the FK that should have been on
			-- carddav_record_state.addressbook_id from the start.
			--
			-- Why: migration 31 created carddav_record_state with
			-- "addressbook_id TEXT NOT NULL" (no FK). When a CardDAV source
			-- (or any one of its addressbooks) is deleted, the existing
			-- source→addressbook CASCADE fires, but nothing cascades from
			-- addressbook to state — every state row becomes an orphan
			-- pointing at a dead addressbook_id, and the contact_records
			-- they reference become unreachable from the source→ab→state→cr
			-- chain that ListRecordIDsForSource walks. The UI sees them
			-- disappear; the rows sit in the DB as zombies indefinitely.
			--
			-- That coverage gap was the underlying cause of two real
			-- failures during 2b.2.b.1 development:
			--   1. The "Enable write access" toggle's save path tore down
			--      and rebuilt addressbooks via UpdateContactSource → every
			--      state row got orphaned in one save.
			--   2. A previously-deleted CardDAV source left 613 zombie
			--      state + record rows behind.
			--
			-- Code-side fixes landed first (UpdateContactSource is now
			-- differential; Store.DeleteSource explicitly scrubs records
			-- before deleting the source). This migration closes the gap
			-- at the schema layer so any future delete path benefits
			-- automatically.
			--
			-- SQLite can't ALTER TABLE to add a FK; we have to rebuild the
			-- table. Pre-step: clean any existing orphans so the rebuild's
			-- INSERT doesn't fail FK validation. This makes the migration
			-- safe for installs that ran the buggy code (i.e., anyone who
			-- developed against 0.3.0-dev between 2b.2.a and 2b.2.b.1).

			-- 1. Drop orphan state rows whose addressbook is gone.
			DELETE FROM carddav_record_state
			WHERE addressbook_id NOT IN (SELECT id FROM contact_source_addressbooks);

			-- 2. Drop orphan contact_records (source='carddav') with no
			--    state row. Bloat from interrupted syncs or past bugs;
			--    they're unreachable via the source→ab→state→cr chain.
			DELETE FROM contact_records
			WHERE source = 'carddav'
			  AND id NOT IN (SELECT record_id FROM carddav_record_state);

			-- 3. Rebuild carddav_record_state with the FK. Copy preserves
			--    the PRIMARY KEY (record_id) and the UNIQUE href constraint
			--    that migration 31 set.
			CREATE TABLE carddav_record_state_new (
				record_id       TEXT PRIMARY KEY REFERENCES contact_records(id) ON DELETE CASCADE,
				addressbook_id  TEXT NOT NULL REFERENCES contact_source_addressbooks(id) ON DELETE CASCADE,
				href            TEXT NOT NULL UNIQUE,
				etag            TEXT,
				synced_at       DATETIME
			);

			INSERT INTO carddav_record_state_new (record_id, addressbook_id, href, etag, synced_at)
			SELECT record_id, addressbook_id, href, etag, synced_at
			FROM carddav_record_state;

			DROP TABLE carddav_record_state;
			ALTER TABLE carddav_record_state_new RENAME TO carddav_record_state;

			-- 4. Recreate the index that migration 31 added (DROP TABLE
			--    above also removed its indexes).
			CREATE INDEX idx_carddav_record_state_addressbook
				ON carddav_record_state(addressbook_id);
		`,
	},
	{
		Version: 34,
		SQL: `
			-- Phase 2b.2.b.2: first-class PHOTO field support on contact_records.
			--
			-- Before this migration, PHOTO data round-tripped through the
			-- vcard_raw preservation mechanism (added in 2b.2.b.1) but was
			-- never extracted, never displayed, never editable. Avatar always
			-- showed initials. This migration adds three columns so the parser
			-- can land photos natively and the builder can emit them under
			-- explicit control.
			--
			-- Invariant: at most one of {photo_data + photo_media_type} OR
			-- photo_url is populated per row. NULL across all three = no photo.
			-- Inline (base64) is the common CardDAV shape (Nextcloud, iCloud,
			-- Apple). URL refs are rarer; we parse + store them but don't
			-- fetch in this phase. Write path always emits inline.

			ALTER TABLE contact_records ADD COLUMN photo_data TEXT;
			ALTER TABLE contact_records ADD COLUMN photo_media_type TEXT;
			ALTER TABLE contact_records ADD COLUMN photo_url TEXT;
		`,
	},
	{
		Version: 35,
		SQL: `
			-- Phase 1B of the Calendar extension introduces the shared
			-- coreapi.Storage.Secrets surface — any first-party extension
			-- can stash per-extension secrets via core without each one
			-- adding its own credentials plumbing.
			--
			-- This table tracks ALL extension secret keys regardless of
			-- where the value actually lives. The encrypted_value column
			-- encodes location: '' (empty) = "lives in OS keyring at
			-- ext:<extension>:<key>"; non-empty = "AES-encrypted base64
			-- ciphertext is right here." Tracking keyring-stored keys in
			-- the table is what lets DeleteAllExtensionSecrets enumerate
			-- the matching keyring entries for cleanup on uninstall.
			--
			-- Owned by core. Not extension-specific (despite the column
			-- name). New extensions opt in via core.Storage().Secrets()
			-- and get keyring-first + table-fallback for free.

			CREATE TABLE IF NOT EXISTS extension_secrets (
				extension       TEXT NOT NULL,
				key             TEXT NOT NULL,
				encrypted_value TEXT NOT NULL DEFAULT '',
				created_at      INTEGER NOT NULL,
				PRIMARY KEY (extension, key)
			);
			CREATE INDEX IF NOT EXISTS idx_extension_secrets_ext ON extension_secrets(extension);
		`,
	},
	{
		Version: 36,
		SQL: `
			-- Per-(account, client_config) encrypted fallback for OAuth tokens.
			--
			-- Before this migration, only the *-mail client_config slots had a
			-- DB fallback when the OS keyring is unavailable — they reuse the
			-- legacy encrypted_access_token / encrypted_refresh_token columns
			-- on the accounts table (migration v9). Non-mail slots (the
			-- extension slots: google-contacts, google-calendar,
			-- microsoft-contacts, microsoft-calendar) had no fallback at all:
			-- when the keyring failed, the StartIncrementalConsent flow
			-- returned "keyring unavailable and no fallback for client config".
			--
			-- This migration extends the per-(account, client_config)
			-- oauth_tokens row with its own encrypted columns so every slot —
			-- mail or extension — gets the same keyring-first +
			-- encrypted-DB-fallback behavior.

			ALTER TABLE oauth_tokens ADD COLUMN encrypted_access_token TEXT;
			ALTER TABLE oauth_tokens ADD COLUMN encrypted_refresh_token TEXT;
		`,
	},
}
