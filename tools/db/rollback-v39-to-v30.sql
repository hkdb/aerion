-- Aerion: rollback the v0.3.0 schema (migrations 31 + 32 + 33 + 34 + 35 + 36 + 37 + 38 + 39) back to v0.2.5 (v30).
--
-- This script reconstructs the v30 schema (`contacts` + `carddav_contacts` tables)
-- from the v39 schema (`contact_records` + `contact_emails` + sidecars +
-- `extension_secrets` + per-slot oauth_tokens encrypted fallback columns +
-- per-account no_outgoing_server / smtp_username / encrypted_smtp_password +
-- reply_forward_identity_id columns + messages.body_failed flag) via JOINs +
-- DROPs / DROP COLUMNs. No external backup file is needed — the unified
-- schema IS the data; the old shape is just a denormalized projection of it.
--
-- Aerion versions and the schemas they ship with:
--   - v0.2.5 (last released) → schema v30 (separate `contacts`, `carddav_contacts`)
--   - v0.3.0 (upcoming)      → schema v39 (unified contact_records + UUID identity
--                                          + carddav_record_state.addressbook_id FK
--                                          + PHOTO columns + extension_secrets
--                                          + per-slot oauth_tokens encrypted fallback
--                                          + per-account no-outgoing-server +
--                                            separate SMTP credential columns
--                                          + reply/forward-with identity preference
--                                          + persistent body-parse-failed flag)
--
-- v31, v32, v33, v34, v35, v36, v37, v38 were intermediate development schemas
-- that never shipped — no real-world DB will ever be at any of them alone. The
-- only rollback path that matters is v39 → v30 (the released-to-released
-- transition).
--
-- Migrations bundled into the 0.3.0 cumulative jump:
--   - 31: unified contact_records + multi-field sub-tables; replaced legacy
--     `contacts` + `carddav_contacts` with the new shape.
--   - 32: rewrote local contact_records IDs from `local-<email>` to UUIDs so
--     local + CardDAV records share the vCard-UID identity model.
--   - 33: added the missing FK from carddav_record_state.addressbook_id to
--     contact_source_addressbooks(id) ON DELETE CASCADE. Closes the privacy
--     gap where deleting a contacts provider left record + state zombies in
--     the local DB.
--   - 34: first-class PHOTO field support — adds photo_data, photo_media_type,
--     photo_url columns to contact_records so the parser/builder land vCard
--     PHOTOs natively (no longer just round-tripping via vcard_raw).
--   - 35: extension_secrets table — shared keyring + AES fallback for the
--     coreapi.Storage.Secrets surface. First consumer is the Calendar
--     extension (1B) for CalDAV passwords. Rolling back drops the table;
--     keyring-stored entries are orphaned (the OS keyring is not touched by
--     this SQL — clear them manually if needed).
--   - 36: per-(account, client_config) encrypted fallback for OAuth tokens —
--     adds encrypted_access_token and encrypted_refresh_token columns to
--     oauth_tokens so non-mail slots (google-contacts, google-calendar,
--     microsoft-contacts, microsoft-calendar) work without an OS keyring.
--     Rolling back drops those columns. The extension-slot rows themselves
--     (where client_config_id != 'google-mail' / 'microsoft-mail') are also
--     deleted, because v0.2.5's OAuth code doesn't understand them and would
--     pick one of them at random when looking up the account's provider.
--   - 37: per-account "No outgoing server" toggle + separate SMTP credentials.
--     Adds no_outgoing_server (INTEGER, default 0), smtp_username (TEXT,
--     default ''), and encrypted_smtp_password (TEXT, nullable) columns to
--     the accounts table. Rolling back drops the three columns. Any account
--     marked receive-only in v0.3.0 will be sendable again under v0.2.5 if
--     its smtp_host is still configured; if smtp_host was left blank, v0.2.5
--     will surface an SMTP error at send time. Separate-SMTP-credential
--     keyring entries (keyed as "<accountID>:smtp") are NOT cleared by this
--     SQL — remove them via the OS keyring manager if you want a clean
--     state. v0.2.5 ignores those entries entirely.
--   - 38: per-account "Reply/Forward with" identity preference for receive-only
--     accounts. Adds reply_forward_identity_id (TEXT, default '') to the
--     accounts table. Only meaningful when no_outgoing_server = 1; sendable
--     accounts use their own identities directly. Rolling back drops the
--     column. Since v0.2.5 has no concept of receive-only accounts (those
--     are the v37 feature this preference depends on), the dropped value
--     wouldn't have applied under v0.2.5 anyway.
--   - 39: persistent "body fetch+parse produced no usable content" flag on the
--     messages table. Adds body_failed (INTEGER, default 0). The previous
--     in-memory cap on parse retries reset every sync session, so
--     unparseable messages re-fetched from IMAP every cycle forever (#240).
--     Rolling back drops the column. Effect on v0.2.5: any message that
--     v0.3.0 had marked unparseable will be re-attempted up to v0.2.5's old
--     limits — same behavior the user used to get before the v0.3.0 fix.
--
-- Inherent data loss on rollback:
--   - Multi-field data (phones, addresses, URLs, IMPPs, org, title, note, bday,
--     nickname, categories) is dropped. v30's schema has no columns for these.
--   - PHOTO data (introduced in v34) is dropped. v30 had no photo storage.
--   - The `vcard_raw` round-trip preservation is dropped (same reason).
--   - CardDAV record IDs are reshaped: each (record, email) pair becomes its own
--     row again, with synthetic IDs of the form `<record_id>:<email>`. Older
--     Aerion identifies contacts during sync by `href` (via GetContactByHref),
--     not by ID, so this works correctly — only the IDs differ.
--   - Local-record UUIDs are reduced back to email-keyed rows. Since v30's
--     `contacts` table was already keyed by email, this is the natural form
--     — the UUIDs were a v32-only concept.
--   - The carddav_record_state FK introduced in v33 is dropped along with the
--     table itself. v0.2.5 has its own (different) cascade behavior on the
--     legacy `carddav_contacts.addressbook_id` FK.
--
-- USAGE
--   1. Quit Aerion completely.
--   2. Back up your aerion.db file just in case:
--        cp ~/.local/share/aerion/aerion.db ~/.local/share/aerion/aerion.db.bak
--      (or whatever your DB path is — `~/Library/Application Support/Aerion/`
--       on macOS, `%LOCALAPPDATA%\aerion\` on Windows).
--   3. Run this script against your DB:
--        sqlite3 ~/.local/share/aerion/aerion.db < rollback-v39-to-v30.sql
--   4. Launch the older Aerion (v0.2.5). It should start normally and your
--      contacts autocomplete should work.
--
-- If anything goes wrong, restore from the backup you made in step 2.

BEGIN TRANSACTION;

-- 1. Recreate legacy `contacts` table with the v30 / pre-v0.3.0 shape.
--    Older Aerion's contact.Store.ensureTable created this table with five
--    columns only (no name_overridden, no kind). Migration 31 was updated
--    to backfill name_overridden / kind from literal defaults rather than
--    selecting them from this table, so there's no longer any reason to
--    preserve those columns through the rollback — they wouldn't survive
--    re-upgrade anyway (migration 31 reruns after rollback clears the
--    migrations tracker at step 8).
CREATE TABLE contacts (
    email TEXT PRIMARY KEY,
    display_name TEXT,
    send_count INTEGER DEFAULT 0,
    last_used DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_contacts_send_count ON contacts(send_count DESC);
CREATE INDEX idx_contacts_last_used ON contacts(last_used DESC);

-- 2. Restore local-contact rows. One row per (record, email) pair where the
--    record is sourced locally. Round-trip is lossless for email / name /
--    send_count / last_used. The kind (manual vs collected) and the
--    name_overridden flag are dropped: the v30 schema has no place to
--    store them, and re-upgrading would substitute literal defaults
--    regardless of what the rollback preserved.
INSERT INTO contacts (email, display_name, send_count, last_used, created_at)
SELECT
    ce.email,
    cr.fn,
    ce.send_count,
    ce.last_used,
    cr.created_at
FROM contact_records cr
JOIN contact_emails ce ON ce.record_id = cr.id
WHERE cr.source = 'local';

-- 3. Recreate legacy `carddav_contacts` table with v30 schema.
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

-- 4. Restore carddav-contact rows. Re-introduces the fan-out: one row per
--    (record, email) pair. Synthetic ID `<record_id>:<email>` ensures uniqueness;
--    older Aerion matches contacts on next sync via (addressbook_id, href).
INSERT INTO carddav_contacts (id, addressbook_id, email, display_name, href, etag, synced_at)
SELECT
    cr.id || ':' || ce.email,
    crs.addressbook_id,
    ce.email,
    cr.fn,
    crs.href,
    crs.etag,
    crs.synced_at
FROM contact_records cr
JOIN contact_emails ce ON ce.record_id = cr.id
JOIN carddav_record_state crs ON crs.record_id = cr.id
WHERE cr.source = 'carddav';

-- 5. Drop the unified tables. Multi-field data is gone after this — same
--    semantics as removing columns from a v30 install that never had them.
DROP TABLE carddav_record_state;
DROP TABLE contact_categories;
DROP TABLE contact_impps;
DROP TABLE contact_urls;
DROP TABLE contact_addresses;
DROP TABLE contact_phones;
DROP TABLE contact_emails;
DROP TABLE contact_records;

-- 6. Drop the v35 extension_secrets table. Any extension secret values stored
--    in this table (encrypted ciphertext) are lost. Keyring-stored entries
--    are NOT touched by this SQL — clear them via the OS keyring manager
--    (Seahorse / Keychain / Credential Manager) if you want a full cleanup.
DROP TABLE IF EXISTS extension_secrets;

-- 7. Roll back v36's per-(account, client_config) OAuth fallback.
--    7a. Delete extension-slot oauth_tokens rows. v0.2.5 only knows about the
--        mail slots; leaving extension rows behind would also leak past v36's
--        purpose. Per-slot keyring entries from these rows (keyed as
--        "<accountID>:<configID>:access_token" / refresh_token) are NOT cleared
--        by this SQL — clear them via the OS keyring manager if desired.
DELETE FROM oauth_tokens
 WHERE client_config_id NOT IN ('google-mail', 'microsoft-mail');

--    7b. Drop the encrypted fallback columns added in v36. SQLite supports
--        DROP COLUMN since 3.35 — modernc.org/sqlite (Aerion's driver) is well
--        past that. If you're on a stock CLI older than 3.35 these will fail;
--        upgrade sqlite3 or skip these two statements (the columns being
--        present is harmless to v0.2.5 — v0.2.5 just ignores unknown columns
--        — but leaving them in place will cause v36's ADD COLUMN to fail on
--        the next upgrade).
ALTER TABLE oauth_tokens DROP COLUMN encrypted_access_token;
ALTER TABLE oauth_tokens DROP COLUMN encrypted_refresh_token;

-- 8. Roll back v37's per-account "No outgoing server" + separate SMTP
--    credentials columns. Same DROP COLUMN caveat as step 7b — needs SQLite
--    >= 3.35. The columns being present is harmless to v0.2.5 (it ignores
--    unknown columns), but leaving them in place will cause v37's ADD COLUMN
--    to fail on the next upgrade. Separate-SMTP keyring entries (keyed as
--    "<accountID>:smtp") are NOT cleared by this SQL — clear them via the
--    OS keyring manager if desired. v0.2.5 doesn't look at them, so the
--    orphaned entries are inert until you upgrade again.
ALTER TABLE accounts DROP COLUMN no_outgoing_server;
ALTER TABLE accounts DROP COLUMN smtp_username;
ALTER TABLE accounts DROP COLUMN encrypted_smtp_password;

-- 9. Roll back v38's "Reply/Forward with" identity preference column. Same
--    DROP COLUMN caveat as step 7b — needs SQLite >= 3.35. The column being
--    present is harmless to v0.2.5 (it ignores unknown columns), but
--    leaving it in place will cause v38's ADD COLUMN to fail on the next
--    upgrade. No keyring or external state to clean up — the column is a
--    pure FK reference to identities.id.
ALTER TABLE accounts DROP COLUMN reply_forward_identity_id;

-- 10. Roll back v39's persistent body-parse-failed flag on messages. Same
--     DROP COLUMN caveat — needs SQLite >= 3.35. v0.2.5 ignores the column
--     if left in place, but leaving it causes v39's ADD COLUMN to fail on
--     re-upgrade. No external state attached — the column was the entire
--     persistence layer for the v39 fix; any message previously marked
--     unparseable will re-enter v0.2.5's old (unbounded) retry behavior,
--     which is the state the user was in before the v0.3.0 fix.
ALTER TABLE messages DROP COLUMN body_failed;

-- 11. Roll back the migration tracker so older Aerion doesn't think v31..v39
--     have been applied. After this, older Aerion sees schema_version=30 and
--     starts normally. The `>= 31` bound catches all v0.3.0 migrations plus
--     any future intermediate schemas.
DELETE FROM migrations WHERE version >= 31;

COMMIT;

-- Verify (optional, run separately to confirm):
--   sqlite3 aerion.db "SELECT COUNT(*) FROM contacts; SELECT COUNT(*) FROM carddav_contacts; SELECT MAX(version) FROM migrations;"
