# Database Rollback Guide

This guide walks you through rolling back Aerion's database schema after an upgrade. Schema migrations in Aerion are **forward-only by design** — the running application doesn't downgrade automatically. If you upgrade Aerion to a new version that ran a migration and then want to go back to an older version, you need to manually reshape the database so the older version can read it.

Each section below covers a single released-to-released schema transition with a documented rollback path. Intermediate development schemas (e.g., the v31 that existed mid-cycle but never shipped) don't get their own section — there's no real-world DB at that state to roll back from. Find the section that matches your release-to-release transition.

## When you might need this

- You upgraded to a newer Aerion (e.g., 0.3.0) and want to go back to 0.2.5 for any reason.
- Aerion shows an error like *"This database was written by a newer Aerion. Either upgrade Aerion or follow the rollback guide..."* — the schema-version gate is refusing to open a DB that's newer than your running build.

## What you'll lose

Each rollback section lists the **data lost on rollback** for that migration. This is inherent: if the newer schema added columns that the older schema doesn't have, those values are dropped when you go back. Anything else round-trips losslessly.

## What you need

- The Aerion DB file. Default path:
  - Linux: `~/.local/share/aerion/aerion.db`
  - macOS: `~/Library/Application Support/Aerion/aerion.db`
  - Windows: `%LOCALAPPDATA%\aerion\aerion.db`
- `sqlite3` command-line tool installed (most Linux/macOS systems have it; Windows users may need to install from sqlite.org).
- The matching rollback script for your migration, downloaded from the Aerion repo's `tools/db/` directory on the branch/tag corresponding to the version that introduced the migration.

---

### Procedure

1. **Quit Aerion completely** (use the menu Quit, or kill the process — make sure nothing is using `aerion.db`).

2. **Back up your DB file** as a precaution. This script makes changes that are difficult to reverse cleanly if anything goes wrong, so a real file copy is your safety net:

   ```bash
   cp ~/.local/share/aerion/aerion.db ~/.local/share/aerion/aerion.db.before-rollback
   ```

   (Adjust the path for your OS — see "What you need" above.)

3. **Download the rollback script** from the Aerion repo. On the branch where the 0.3.0 schema lives (0.3.0 or later):

   ```bash
   curl -O https://raw.githubusercontent.com/hkdb/aerion/main/tools/db/rollback-v39-to-v30.sql
   ```

   (Or download via your browser from `https://github.com/hkdb/aerion/blob/main/tools/db/rollback-v39-to-v30.sql`.)

4. **Run the script against your DB**:

   ```bash
   sqlite3 ~/.local/share/aerion/aerion.db < rollback-v39-to-v30.sql
   ```

   The script runs in a single transaction. If anything fails, no changes are committed and your DB is unchanged.

5. **Verify the rollback** worked:

   ```bash
   sqlite3 ~/.local/share/aerion/aerion.db \
     "SELECT COUNT(*) FROM contacts; SELECT COUNT(*) FROM carddav_contacts; SELECT MAX(version) FROM migrations;"
   ```

   You should see your contacts counts and `30` as the max migration version.

6. **Launch the older Aerion** (0.2.5 or earlier). It should start normally and your contacts autocomplete should work as before.

### If something goes wrong

Restore the backup you made in step 2:

```bash
cp ~/.local/share/aerion/aerion.db.before-rollback ~/.local/share/aerion/aerion.db
```

You're back to the v39 state and can run Aerion 0.3.0 again.

If the issue persists, open a GitHub issue with the SQL error output and the version you were rolling back from / to.

---

## Rollback: v39 → v30 (Aerion 0.3.0 → 0.2.5)

**Introduced in**: Aerion 0.3.0 (cumulative effect of migrations 31 + 32 + 33 + 34 + 35 + 36 + 37 + 38 + 39 — see notes below).

**What 0.3.0 changed since 0.2.5**:

- **Migration 31** (Phase 2b.2.a): Replaced the legacy denormalized `contacts` (autocomplete-by-email) and `carddav_contacts` (per-email fan-out) tables with a unified `contact_records` schema covering both local and CardDAV contacts. Added multi-field support (phones, addresses, URLs, IMPPs, organization, title, notes, birthday, nickname, categories) and the `vcard_raw` round-trip column.
- **Migration 32**: Switched local-record IDs from the synthetic `"local-<email>"` shape (a leftover from the v30 email-as-PK schema) to UUIDs. Brings local records in line with CardDAV's vCard-UID identity semantics — emails become fully editable sub-rows in `contact_emails` rather than encoded into the record id.
- **Migration 33** (Phase 2b.2.b.1 follow-up): Added a missing `FOREIGN KEY ... ON DELETE CASCADE` from `carddav_record_state.addressbook_id` to `contact_source_addressbooks(id)`. Closes the privacy gap where deleting a contacts provider left record + state zombies in the local DB. Pre-step cleans any pre-existing orphans so the FK rebuild succeeds.
- **Migration 34** (Phase 2b.2.b.2): First-class PHOTO field support — adds `photo_data`, `photo_media_type`, `photo_url` columns to `contact_records` so the vCard parser/builder can extract and emit PHOTO natively. Before v34, photos round-tripped opaquely via `vcard_raw` but were never displayed.
- **Migration 35** (Calendar extension Phase 1B): Adds the `extension_secrets` table — shared keyring + AES fallback storage for the new `coreapi.Storage.Secrets` surface. First consumer is the Calendar extension's CalDAV password storage. The table tracks all extension secret keys regardless of where the value lives (empty `encrypted_value` = "in OS keyring", non-empty = "AES ciphertext right here").
- **Migration 36** (Extension OAuth keyring-fallback): Adds `encrypted_access_token` and `encrypted_refresh_token` columns to `oauth_tokens` so non-mail OAuth slots (`google-contacts`, `google-calendar`, `microsoft-contacts`, `microsoft-calendar`) can persist tokens on systems where the OS keyring isn't available — previously only the mail slots had an encrypted-DB fallback (via the `accounts` table from v9).
- **Migration 37** (Per-account "No outgoing server" + separate SMTP credentials): Adds `no_outgoing_server` (INTEGER, default 0), `smtp_username` (TEXT, default `''`), and `encrypted_smtp_password` (TEXT, nullable) columns to the `accounts` table. Lets users mark an account as receive-only (hidden from the composer's From dropdown, send attempts blocked), and lets Generic-provider accounts authenticate SMTP with credentials separate from IMAP. The keyring carries the separate SMTP password under `<accountID>:smtp` when set; `encrypted_smtp_password` is the AES-fallback companion for systems without an OS keyring.
- **Migration 38** (Per-account "Reply/Forward with" identity preference): Adds `reply_forward_identity_id` (TEXT, default `''`) to the `accounts` table. For receive-only accounts (the v37 feature), lets the user pick a specific identity from another sendable account to pre-select in the composer when replying or forwarding messages received here. Empty value falls back to the user's default sending account, then to the first available identity. Sendable accounts ignore the column entirely.
- **Migration 39** (Persistent body-parse-failed flag): Adds `body_failed` (INTEGER, default 0) to the `messages` table. Set to 1 when a body fetch+parse produced no usable content (and the message isn't encrypted, which legitimately has empty plaintext until view-time decryption). Replaces an in-memory cap on parse retries that reset every sync session — so an unparseable message used to be re-fetched from IMAP on every cycle, forever (issue #240). The body-fetch queue now excludes any row with `body_failed = 1`. A future parser improvement can re-queue previously-skipped messages via a one-line `UPDATE messages SET body_failed = 0 WHERE …` migration.

Migrations 31 through 39 ship together in 0.3.0 — no real-world DB will ever stop between them. The rollback script below handles the cumulative v39 state, which is what your DB will be in after upgrading from 0.2.5.

**Data lost on rollback to v30**:

- All multi-field contact data — phone numbers, addresses, URLs, instant-messaging handles, organization, job title, notes, birthday, nickname, categories. The v30 schema has no columns for these, so they're dropped.
- The `vcard_raw` round-trip preservation column. Means that the next time CardDAV sync runs under the older Aerion, it will re-fetch and re-parse vCards from the server — only the fields the older parser knows about (email + display name) survive.
- CardDAV contacts' synthetic local IDs are reshaped. Older Aerion identifies CardDAV contacts during sync by `href` (server-side URL path), not by local ID, so this doesn't affect sync correctness — only the row IDs change.
- Local-record UUIDs are dropped — v30 keys local contacts by email, which is the natural identity for the legacy schema.
- **Extension secrets stored in the AES-fallback path** (i.e., entries in the `extension_secrets` table). Keyring-stored entries are NOT touched by the rollback SQL — they remain in the OS keyring but become orphaned (no DB row pointing at them). To clean them up, use your OS keyring manager (Seahorse / Keychain / Credential Manager) and remove entries starting with `ext:`. In practice the Calendar extension is the only Phase-1 consumer, so the impact is: any saved CalDAV passwords will need to be re-entered after rollback + upgrade.
- **Per-extension OAuth grants** (the `google-contacts`, `google-calendar`, `microsoft-contacts`, `microsoft-calendar` slot tokens). The rollback deletes those `oauth_tokens` rows and drops the new fallback columns. v0.2.5 doesn't have the contacts/calendar extensions, so this only matters when you upgrade back to 0.3.0 — you'll need to re-grant calendar / contacts access from inside the relevant extension setting. Per-slot keyring entries (keys of the form `<accountID>:<configID>:access_token` / `refresh_token`) are NOT cleared by the rollback SQL; remove them from the OS keyring manager if you want a clean state. The mail OAuth grant (the `google-mail` / `microsoft-mail` row) is untouched.
- The local-contact `kind` (`manual` vs. `collected`) and the `name_overridden` flag. The v30 / pre-v0.3.0 `contacts` table has no columns for these (older `ensureTable` never created them). Re-upgrading after rollback reruns migration 31, which backfills both from literal defaults (`'collected'` / `0`) regardless of what the rollback would have stored, so preservation through the round-trip isn't possible. Practically: contacts you marked as manually-added in v0.3.0 will look auto-collected after a full round-trip; user-edited names auto-collected from sent mail won't be protected from being overwritten on the next auto-collection until you re-mark them.
- **Per-account "No outgoing server" + separate SMTP credentials state** (v37). The `no_outgoing_server`, `smtp_username`, and `encrypted_smtp_password` columns are dropped. Any account you marked receive-only in v0.3.0 will become sendable again under v0.2.5 if it still has a valid `smtp_host`; if SMTP host was left blank, v0.2.5 will surface an SMTP error at send time instead. Accounts that used separate SMTP credentials revert to using the IMAP username + IMAP password for SMTP AUTH, which is what v0.2.5 has always done. Separate-SMTP keyring entries (keys of the form `<accountID>:smtp`) are NOT cleared by the rollback SQL; remove them from the OS keyring manager if you want a clean state — v0.2.5 ignores them entirely. After re-upgrading to v0.3.0, you'll need to re-enter any separate SMTP passwords (the `<accountID>:smtp` keyring entry survives if you didn't manually clear it, in which case the toggle still works without re-entry).
- **Per-account "Reply/Forward with" identity preference** (v38). The `reply_forward_identity_id` column is dropped. v0.2.5 has no concept of receive-only accounts (that's the v37 feature this preference depends on), so the dropped value wouldn't have applied under v0.2.5 anyway — there's no behavior change. On re-upgrade to v0.3.0, accounts that had this preference set will revert to the empty default (i.e., the composer will use the user's default sending account when replying/forwarding on a receive-only account); the user re-picks the identity in the account's Server tab.
- **Persistent body-parse-failed flag** (v39). The `body_failed` column is dropped. Effect under v0.2.5: every message that v0.3.0 had marked unparseable will be subject to v0.2.5's old (unbounded) re-fetch behavior — the same state the user was in before #240 was fixed. Bounded only by message count, not by sync cycles. On re-upgrade to v0.3.0, those messages will be re-attempted up to one time and re-flagged via the new persistence path; transient, self-correcting.

**OAuth Credentials picker leftover state (inert, not cleaned by the rollback)**:

The v0.3.0 OAuth Credentials picker (Settings → Accounts → OAuth Credentials, plus the equivalent extension settings sections) accumulates state in three places that the rollback SQL doesn't touch. All three are inert under v0.2.5 — the older code doesn't read them — so leaving them in place is safe. They're listed here in case you want a fully clean state, or are doing a re-upgrade and want to start fresh:

- The `user_oauth_clients` table (on-demand, created when the user first saves a Custom client_id+secret). The table itself stays; rows stay. v0.2.5 doesn't read it. On re-upgrade to v0.3.0, the saved values become active again.
- The `user_oauth_slot_aliases` table (on-demand, created when the user first picks the "Aerion - <provider>" alias option). Same treatment.
- Per-slot rows in the existing `settings` table with key `oauth_active_choice:<slot_id>` (introduced post-v0.3.0-build1). These encode the user's explicit picker selection independent of which credentials/alias rows happen to exist. v0.2.5 doesn't read them. On re-upgrade to v0.3.0, the marker takes effect again and the picker reflects what was last selected.

Optional cleanup (run only if you want zero leftover OAuth picker state in the DB):

```sql
DROP TABLE IF EXISTS user_oauth_clients;
DROP TABLE IF EXISTS user_oauth_slot_aliases;
DELETE FROM settings WHERE key LIKE 'oauth_active_choice:%';
```

Per-slot keyring entries (keyed as `oauth_user_client:<configID>`) are NOT cleared by SQL — remove them via the OS keyring manager (Seahorse / Keychain / Credential Manager) if you want a full external cleanup.

**What round-trips losslessly**:

- All emails and display names.
- Send-count and last-used autocomplete metadata (per-email).
- CardDAV addressbook membership, href, and ETag (for re-sync identity).

