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
   curl -O https://raw.githubusercontent.com/hkdb/aerion/main/tools/db/rollback-v34-to-v30.sql
   ```

   (Or download via your browser from `https://github.com/hkdb/aerion/blob/main/tools/db/rollback-v34-to-v30.sql`.)

4. **Run the script against your DB**:

   ```bash
   sqlite3 ~/.local/share/aerion/aerion.db < rollback-v34-to-v30.sql
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

You're back to the v34 state and can run Aerion 0.3.0 again.

If the issue persists, open a GitHub issue with the SQL error output and the version you were rolling back from / to.

---

## Rollback: v34 → v30 (Aerion 0.3.0 → 0.2.5)

**Introduced in**: Aerion 0.3.0 (cumulative effect of migrations 31 + 32 + 33 + 34 — see notes below).

**What 0.3.0 changed since 0.2.5**:

- **Migration 31** (Phase 2b.2.a): Replaced the legacy denormalized `contacts` (autocomplete-by-email) and `carddav_contacts` (per-email fan-out) tables with a unified `contact_records` schema covering both local and CardDAV contacts. Added multi-field support (phones, addresses, URLs, IMPPs, organization, title, notes, birthday, nickname, categories) and the `vcard_raw` round-trip column.
- **Migration 32**: Switched local-record IDs from the synthetic `"local-<email>"` shape (a leftover from the v30 email-as-PK schema) to UUIDs. Brings local records in line with CardDAV's vCard-UID identity semantics — emails become fully editable sub-rows in `contact_emails` rather than encoded into the record id.
- **Migration 33** (Phase 2b.2.b.1 follow-up): Added a missing `FOREIGN KEY ... ON DELETE CASCADE` from `carddav_record_state.addressbook_id` to `contact_source_addressbooks(id)`. Closes the privacy gap where deleting a contacts provider left record + state zombies in the local DB. Pre-step cleans any pre-existing orphans so the FK rebuild succeeds.
- **Migration 34** (Phase 2b.2.b.2): First-class PHOTO field support — adds `photo_data`, `photo_media_type`, `photo_url` columns to `contact_records` so the vCard parser/builder can extract and emit PHOTO natively. Before v34, photos round-tripped opaquely via `vcard_raw` but were never displayed.

Migrations 31, 32, 33, and 34 ship together in 0.3.0 — no real-world DB will ever stop between them. The rollback script below handles the cumulative v34 state, which is what your DB will be in after upgrading from 0.2.5.

**Data lost on rollback to v30**:

- All multi-field contact data — phone numbers, addresses, URLs, instant-messaging handles, organization, job title, notes, birthday, nickname, categories. The v30 schema has no columns for these, so they're dropped.
- The `vcard_raw` round-trip preservation column. Means that the next time CardDAV sync runs under the older Aerion, it will re-fetch and re-parse vCards from the server — only the fields the older parser knows about (email + display name) survive.
- CardDAV contacts' synthetic local IDs are reshaped. Older Aerion identifies CardDAV contacts during sync by `href` (server-side URL path), not by local ID, so this doesn't affect sync correctness — only the row IDs change.
- Local-record UUIDs are dropped — v30 keys local contacts by email, which is the natural identity for the legacy schema.

**What round-trips losslessly**:

- All emails and display names.
- Send-count and last-used autocomplete metadata (per-email).
- The `name_overridden` flag that prevents auto-collection from overwriting user-edited names.
- The local-contact `kind` (`manual` vs. `collected`).
- CardDAV addressbook membership, href, and ETag (for re-sync identity).

