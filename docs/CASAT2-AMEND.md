# CASA Tier 2 — Amendments to the Self-Assessment Record

[`CASAT2.md`](CASAT2.md) is the record of what was answered in the last CASA Tier 2 audit and is not edited after the fact. This file lists changes made since that audit that affect its answers, so the next assessment can start from an accurate picture.

---

## 2026-10-20 — §17 Debug Mode: additional logging opt-ins

**Change.** Two command-line flags and two environment variables were added alongside the existing `--debug` / `AERION_DEBUG=1`:

| Option | Effect |
|---|---|
| `--log-level <fatal\|error\|warn\|info\|debug>` / `AERION_LOG_LEVEL` | Selects the log level. Default remains `fatal`. `--debug` / `AERION_DEBUG=1` still force `debug` and take precedence. |
| `--log-file <path>` / `AERION_LOG_FILE` | Additionally writes log lines to the given file. Off by default. |

**Defaults unchanged.** With no flags or variables set, behaviour is identical to the audited state: fatal-only output to stderr, no file. Production and Flatpak builds set none of these.

**Log file properties.**
- Created by the application at the user-supplied path with mode `0600`; parent directories are created with mode `0700`.
- Human-readable console format (no colour codes), the same lines that would appear on stderr.
- Capped at 10 MB; on overflow the file is renamed to `<path>.1` (replacing any previous `.1`) and a fresh file is started. At most two generations exist on disk.
- A detached composer process receives the same level and writes to a sibling file (`<name>.composer.<ext>`), never to the main file.

**Debug output safety.** The statement in §17 that log output does not contain passwords, OAuth tokens, private keys, or email body content now also applies to the persistent log file, since it carries exactly the same lines. No new log content was introduced by this change beyond operational timing and lifecycle events.

**Level tiers.** Some existing debug lines (one-per-operation lifecycle events such as a scheduled sync starting or finishing, an IDLE connection being replaced, an OAuth token refresh) were moved to `info` so that level is usable without the full `debug` firehose. Per-message and per-command lines remain at `debug`.
