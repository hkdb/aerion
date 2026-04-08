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
}
