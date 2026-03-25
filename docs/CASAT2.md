# CASA Tier 2 — Self-Assessment Questionnaire

This document provides detailed responses to the Cloud Application Security Assessment (CASA) Tier 2 Self-Assessment Questionnaire for [Aerion](https://github.com/hkdb/aerion), a lightweight cross-platform email client.

---

## 1. Trust Boundaries, Components, and Data Flows

### Trust Boundaries

| Boundary | From | To | Transport |
|---|---|---|---|
| UI ↔ Backend | Svelte frontend (WebView) | Go backend | Wails RPC (same-process, not network-exposed) |
| Backend ↔ Mail Servers | Go backend | IMAP/SMTP servers | TLS / STARTTLS |
| Backend ↔ OAuth2 Providers | Go backend | Google / Microsoft | HTTPS with PKCE |
| Backend ↔ OS Keyring | Go backend | Secret Service API (Linux) / Keychain (macOS) / Credential Manager (Windows) | OS IPC |
| Backend ↔ Local Database | Go backend | SQLite (WAL mode) | File I/O, 0600 permissions |
| Main Window ↔ Composer | Main process | Detached composer process | Unix socket (Linux/macOS) / Named pipe (Windows), token-authenticated |
| Backend ↔ CardDAV Servers (optional) | Go backend | CardDAV endpoints | HTTPS |
| Backend ↔ Key Servers (optional) | Go backend | HKP / WKD servers | HTTPS |

### Components

- **Frontend**: Svelte 5 application rendered in a Wails WebView (WebKit on Linux/macOS, WebView2 on Windows). Handles UI, user interaction, and display.
- **Backend**: Go application exposing methods to the frontend via Wails bindings. Manages all business logic, synchronization, cryptographic operations, and external service communication.
- **SQLite Database**: Local storage for messages, accounts, folders, contacts, drafts, settings, certificates, and PGP keys. WAL mode for concurrent reads. File permissions restricted to owner (0600).
- **OS Keyring**: Primary credential storage for passwords, OAuth tokens, and (when enabled) S/MIME private keys and PGP private keys.
- **Encrypted DB Fallback**: When OS keyring is unavailable, credentials are stored in the database encrypted with XChaCha20-Poly1305.
- **IPC System**: Token-authenticated Unix socket (Linux/macOS) or named pipe (Windows) for communication between the main window and detached composer windows.

### Significant Data Flows

**Core (all users):**

1. **Email Sync (Inbound)**: IMAP server → TLS → Go backend (parse MIME, extract attachments) → SQLite database
2. **Email Send (Outbound)**: Composer → Go backend → TLS → SMTP server
3. **Credential Storage**: User input → OS keyring (primary) or XChaCha20-Poly1305 encrypted DB (fallback)
4. **OAuth2 Flow**: User initiates → local callback server on random port → authorization code with PKCE → token exchange over HTTPS → encrypted token storage
5. **HTML Rendering**: Raw email HTML → bluemonday sanitization (strip scripts, dangerous tags, tracking pixels) → WebView display
6. **Detached Composer**: Composer process ↔ token-authenticated IPC ↔ main process (for sending, draft save/delete)

**Optional (user-enabled per account):**

7. **S/MIME Signing/Encryption**: When enabled, outbound messages are signed and/or encrypted via CMS/PKCS#7. Inbound signed messages are verified automatically. Requires user-imported PKCS#12 certificate.
8. **PGP Signing/Encryption**: When enabled, outbound messages are signed and/or encrypted via PGP/MIME. Inbound signed messages are verified automatically. Requires user-imported PGP keypair.
9. **PGP Key Discovery**: WKD (direct + advanced methods) → HKP (sequential key servers) → HTTPS → Go backend → SQLite. Only triggered by explicit user action.
10. **CardDAV Contact Sync**: CardDAV server → HTTPS → Go backend → SQLite. Only active when user configures a CardDAV source.

---

## 2. Client-Side Technologies

Aerion does not use any unsupported, insecure, or deprecated client-side technologies. Specifically, there is no use of:

- NSAPI plugins
- Adobe Flash or Shockwave
- Microsoft ActiveX or Silverlight
- Google NaCl (Native Client)
- Client-side Java applets

The frontend is a **Svelte 5** single-page application rendered inside a platform-native WebView provided by the [Wails](https://wails.io/) framework:

- **Linux / macOS**: WebKit (WebKitGTK / WKWebView)
- **Windows**: Microsoft WebView2 (Chromium-based)

All UI rendering uses standard HTML5, CSS, and JavaScript. Communication between the frontend and Go backend occurs via Wails' in-process RPC bindings — no network sockets, browser plugins, or legacy technologies are involved.

---

## 3. Access Control Enforcement

Aerion is a local desktop application. All access controls are enforced in the **Go backend** — never in the frontend (Svelte/WebView). The frontend acts solely as a presentation layer and has no direct access to external services, local files, or credential storage.

**Backend-enforced controls:**

- **Authentication**: OAuth2 flows (with PKCE and state validation), IMAP/SMTP credential handling, and token exchange are all performed by the Go backend. The frontend never sees raw credentials or tokens.
- **Credential Storage**: Passwords, OAuth tokens, S/MIME private keys, and PGP private keys are stored via the OS keyring or an encrypted database fallback (XChaCha20-Poly1305). Access is managed entirely by the backend.
- **File Permissions**: The SQLite database (0600) and data directories (0700) are created and enforced by the backend at the OS level.
- **IPC Authentication**: Communication between the main window and detached composer processes requires a token generated by the backend. Unauthenticated IPC connections are rejected.
- **Email Content Sanitization**: HTML email content is sanitized server-side (Go backend, bluemonday) before being passed to the frontend for display.
- **Single Instance Lock**: The backend enforces single-instance via OS-level locks (Unix socket on Linux/macOS, named mutex on Windows), preventing unauthorized parallel access to the database.

---

## 4. Sensitive Data Classification

All sensitive data in Aerion is identified and classified into the following protection levels:

### Critical — OS Keyring Protected

Stored in the OS keyring (Linux Secret Service API, macOS Keychain, Windows Credential Manager) with an encrypted database fallback (XChaCha20-Poly1305) when the keyring is unavailable:

- Account passwords (IMAP/SMTP)
- OAuth2 access and refresh tokens
- S/MIME private keys (when enabled)
- PGP private keys (when enabled)

### High — Encrypted or Access-Restricted

- **SQLite database** (0600 file permissions, owner-only): contains email messages, headers, contacts, drafts, account configurations, certificates, and PGP public keys
- **Encrypted drafts**: when S/MIME or PGP is enabled, draft message bodies are encrypted to self before database storage
- **Data directories** (0700 permissions): config, data, and cache directories
- **IPC socket** (0600 permissions): inter-process communication channel

### Medium — Sanitized Before Use

- **Inbound email HTML**: sanitized with bluemonday (scripts, dangerous tags, and tracking pixels stripped) before display
- **Remote images**: blocked by default, user-controlled allowlist per sender

### Non-Sensitive

- UI state (pane sizes, sidebar width)
- Application settings (theme, language, sync interval)
- Folder structure and message counts

---

## 5. Protection Requirements

Each data classification level defined in [Section 4](#4-sensitive-data-classification) has associated protection requirements that are enforced in the architecture:

| Level | Encryption | Integrity | Access Control | Retention | Privacy |
|---|---|---|---|---|---|
| **Critical** | OS keyring encryption or XChaCha20-Poly1305 encrypted DB fallback | Keyring/OS-managed | OS-level keyring access control | User-controlled (delete account removes credentials) | Never logged, never transmitted in plaintext |
| **High** | Encrypt-to-self for drafts (S/MIME CMS or PGP/MIME, when enabled) | SQLite WAL mode with checksums | File permissions: DB 0600, directories 0700, IPC socket 0600 | User-controlled (all data is local, deletable) | No telemetry, no cloud sync, no third-party data sharing |
| **Medium** | N/A (display-only after sanitization) | Sanitization via bluemonday before rendering | Backend-enforced sanitization; frontend cannot bypass | Transient (rendered in WebView, not persisted separately) | Tracking pixels and beacons stripped; remote images blocked by default |
| **Non-Sensitive** | None required | SQLite storage | Standard file permissions | Persisted locally, user-deletable | No external transmission |

**Key architectural enforcement points:**

- **No telemetry or analytics**: Aerion collects no usage data and makes no network requests beyond user-configured mail/contact/key servers and OAuth providers.
- **All storage is local**: No cloud backend, no server-side component. The user has full control over data retention and deletion.
- **Credential isolation**: The frontend (WebView) never has direct access to credentials, private keys, or tokens. All sensitive operations go through the Go backend.

---

## 6. Integrity Protections

### Embedded Frontend Assets

All frontend assets (HTML, CSS, JavaScript) are embedded into the compiled Go binary at build time using Go's `//go:embed` directive (`main.go`). At runtime, the Wails WebView serves these assets from the embedded filesystem — no files are loaded from disk or fetched from the network. There is no mechanism to inject, replace, or modify frontend code after compilation.

### No Remote Code Loading

Aerion does not load or execute code from untrusted or external sources at runtime:

- No CDN-hosted scripts or stylesheets
- No dynamic plugin/module loading
- No remote includes or hot-code updates
- No `eval()` or dynamic script injection

### Dependency Integrity

- **Go dependencies**: Locked and verified via `go.sum` (cryptographic checksums for all modules)
- **Node.js dependencies**: Locked via `package-lock.json` with integrity hashes

### Code Signing

- **macOS**: Builds are ad-hoc code-signed (`codesign --force --deep --sign -`) as required by macOS for notification and security APIs
- **Flatpak**: Distributed via Flathub, which enforces GPG-signed repositories and verified builds

### Email Content Isolation

Inbound HTML email is sanitized server-side by the Go backend using bluemonday before being passed to the WebView. All `<script>` tags, event handlers, and dangerous elements are stripped. Remote images are blocked by default. This prevents untrusted email content from executing code within the application.

---

## 7. Subdomain Takeover Protection

Aerion is a **local desktop application** with no web infrastructure. It does not operate or depend on:

- Custom DNS entries or subdomains
- Cloud APIs, serverless functions, or storage buckets
- Transient or auto-generated cloud hostnames
- DNS CNAMEs or pointers to third-party services

The only external DNS dependencies are:

- **github.com/hkdb/aerion** — Source code repository and release distribution (actively maintained by the project owner)
- **Flathub (flathub.org)** — Flatpak package distribution (maintained by the Flathub organization)

Neither of these involves project-owned DNS records or subdomains that could be subject to takeover. The application itself connects only to user-configured mail servers, OAuth providers (Google/Microsoft), and optionally CardDAV/key servers — all specified by the end user, not hardcoded infrastructure.

---

## 8. Anti-Automation Controls

Aerion is a **local desktop application** that does not expose any network-accessible APIs, endpoints, or services. There is no attack surface for remote automation, mass data exfiltration, or denial-of-service attacks against the application itself.

The following built-in controls prevent excessive or abusive local operations:

- **Single-instance enforcement**: Only one instance of Aerion can run at a time (Unix socket lock on Linux/macOS, named mutex on Windows), preventing parallel automated access to the database.
- **IMAP connection pooling**: Maximum 3 concurrent connections per account with a 5-minute idle timeout, preventing excessive connections to mail servers.
- **Batch size limits**: Header fetching is capped at 50 messages per batch; body fetching is capped at 512KB or 50 messages per batch. Individual MIME parts are limited to 10MB, and total message size is limited to 50MB.
- **Sync retry limits**: Failed message fetches are retried a maximum of 3 times before being skipped.
- **OAuth2 provider-side rate limits**: OAuth token exchange and refresh are subject to Google/Microsoft rate limiting on the provider side.
- **IPC token authentication**: Detached composer windows must authenticate with a backend-generated token, preventing unauthorized processes from issuing commands via the IPC channel.

---

## 9. Untrusted File Storage

Aerion is a desktop application with no web server or web root. There is no directory served over HTTP that untrusted files could be placed into.

**How untrusted data is handled:**

- **Email messages and attachments**: Stored as structured data within the SQLite database (0600 file permissions, owner-only). Attachments are stored as metadata references; binary content is fetched from the IMAP server on demand.
- **Attachment downloads**: When a user explicitly downloads an attachment, it is saved to a location chosen by the user via the OS file picker. The application does not auto-save or auto-execute attachments.
- **Email HTML content**: Sanitized server-side by the Go backend (bluemonday) before display. Scripts, event handlers, and dangerous elements are stripped. This content is rendered in the WebView but never written to disk as standalone files.
- **Data directories**: All application data directories are created with 0700 permissions (owner-only access):
  - Linux: `~/.local/share/aerion/` (XDG Base Directory spec)
  - macOS: `~/Library/Application Support/Aerion/`
  - Windows: `%LOCALAPPDATA%\aerion\`

---

## 10. Malicious Content Scanning

Aerion is a desktop email client that does not upload or serve files to other users. There is no file hosting, sharing, or serving component.

**How untrusted content is handled:**

- **No auto-download or auto-execute**: Email attachments are not automatically downloaded to disk or executed. Attachment content is fetched from the IMAP server on demand only when the user explicitly requests a download.
- **User-initiated downloads**: Attachments are saved to a user-chosen location via the OS file picker. Once on disk, they are subject to the operating system's antivirus or endpoint protection (e.g., Windows Defender, macOS XProtect, or user-installed Linux AV solutions).
- **HTML sanitization**: Inbound email HTML is sanitized by the Go backend using bluemonday before rendering. All scripts, event handlers, iframes, and dangerous elements are stripped, preventing execution of malicious content embedded in email bodies.
- **Tracking protection**: Tracking pixels, beacons, and remote images are blocked by default. Users can allowlist specific senders for remote image loading.
- **TNEF handling**: Outlook winmail.dat (TNEF) attachments are parsed server-side to extract embedded files, which follow the same on-demand download and sanitization controls.

---

## 11. API URL Security

Aerion does not expose any API endpoints of its own. Where it consumes external APIs, no sensitive information is included in URLs.

**Internal communication:**

- **Frontend ↔ Backend**: Uses Wails in-process RPC bindings (function calls within the same process). No HTTP requests, no URLs, no query strings. Credentials and tokens never appear in any URL.
- **IPC**: Uses Unix domain sockets (Linux/macOS) or named pipes (Windows) with token authentication. No network URLs.

**External API consumption (all via Go backend):**

- **Google Contacts API** (`people.googleapis.com`): OAuth2 bearer token passed in `Authorization` header, not in query strings. URLs contain only non-sensitive parameters (field masks, page size, search queries).
- **Microsoft Graph API** (`graph.microsoft.com`): OAuth2 bearer token passed in `Authorization` header. URLs contain only non-sensitive parameters.
- **CardDAV servers**: HTTP Basic or OAuth2 credentials passed in `Authorization` header, not in URLs.
- **HKP key servers / WKD**: Public key lookups by email address — no authentication required, no sensitive data in URLs.

**OAuth2 callback:**

The local OAuth2 callback server listens on a random port on `localhost` and only receives the authorization code and state parameter. Access tokens and refresh tokens are exchanged server-side (Go backend → provider HTTPS endpoint) and never appear in redirect URLs or browser history. The state parameter is validated against a PKCE challenge generated per-flow.

**IMAP/SMTP**: Binary protocols over TLS — no HTTP URLs involved.

---

## 12. Authorization Decisions

Aerion is a local desktop application with no HTTP server, URI routing, or multi-user access model. There are no URIs, controllers, or routers to enforce authorization against. However, authorization decisions are enforced at every access boundary:

**Process-level authorization:**

- **Single-instance lock**: Only one Aerion process can access the database at a time (Unix socket lock on Linux/macOS, named mutex on Windows).
- **IPC token authentication**: Detached composer processes must present a backend-generated token to communicate with the main process. Unauthenticated connections are rejected.

**Resource-level authorization:**

- **OS keyring**: Credentials, private keys, and OAuth tokens are protected by the operating system's keyring access controls (user session scope).
- **File permissions**: Database files (0600) and data directories (0700) are restricted to the owner, enforced by the OS filesystem.
- **Wails RPC boundary**: The frontend (WebView) can only invoke methods explicitly exposed by the Go backend via Wails bindings. There is no mechanism for the frontend to directly access the database, filesystem, or OS keyring.

**External service authorization:**

- **IMAP/SMTP**: Authenticated per-account using credentials stored in the keyring (password or OAuth2 XOAUTH2 SASL).
- **OAuth2 providers**: Authorization enforced by Google/Microsoft using scoped tokens obtained via PKCE flow.
- **CardDAV / Key servers**: Authenticated using account-specific credentials passed in HTTP Authorization headers.

---

## 13. RESTful HTTP Methods

Aerion does not expose any RESTful HTTP APIs, endpoints, or web services. There are no HTTP routes, controllers, or method handlers to restrict.

**Internal communication**: The frontend communicates with the Go backend exclusively via Wails in-process RPC (direct function calls), not HTTP requests.

**External API consumption**: Where the Go backend consumes external REST APIs, it uses the appropriate HTTP methods as required by each provider's API specification:

- **Google Contacts API**: GET for reading contacts and search
- **Microsoft Graph API**: GET for reading contacts (with delta sync)
- **CardDAV**: Standard WebDAV/CardDAV methods (PROPFIND, REPORT, GET) as required by the protocol
- **HKP key servers**: GET for public key lookups
- **OAuth2 token exchange**: POST as required by the OAuth2 specification

All external HTTP calls are made server-side by the Go backend. The frontend has no ability to make direct HTTP requests to external services.

---

## 14. Secure Build and Deployment

### Build Process

Aerion uses a **Makefile** with well-defined, repeatable build targets:

- `make build` — Production binary with compile-time ldflags
- `make build-linux` — Linux-specific build with production tags
- `make flatpak` — Flatpak package build
- `make test` — Automated test suite
- `make lint` / `make fmt` — Code quality checks and formatting

All build steps are deterministic and automated — no manual intervention required beyond running the appropriate make target.

### Dependency Integrity

- **Go modules**: All dependencies pinned in `go.mod` with cryptographic checksums verified via `go.sum`
- **Node.js packages**: All dependencies pinned in `package-lock.json` with integrity hashes
- **Flatpak builds**: Use `flatpak-builder` with vendored dependencies for offline, reproducible builds

### CI/CD and Distribution

- **Flathub**: Flatpak distribution uses Flathub's automated build infrastructure, which builds from source in a sandboxed environment with GPG-signed repositories
- **GitHub Actions**: Used for automated release workflows
- **Source builds**: Flathub builds from source using the project's manifest (`io.github.hkdb.Aerion.yml`), ensuring the distributed binary matches the published source code

### Secrets Management

- **OAuth client secrets**: Injected at compile time via environment variables and ldflags — never hardcoded in source code
- **`.env` / `.env.local`**: Used only for local development builds, listed in `.gitignore`
- **No secrets in repository**: Private credentials and signing keys are excluded from version control

---

## 15. Automated Redeployment

### Rebuildable from Source

Aerion can be fully rebuilt from source at any time using documented, automated build commands:

- `make build` — Build production binary
- `make build-linux` — Linux-specific production build
- `make flatpak` — Build Flatpak package locally

All dependencies are pinned with integrity verification (`go.sum`, `package-lock.json`), ensuring reproducible builds. The build process is documented in `docs/BUILD.md` and the project `CLAUDE.md`.

### Distribution Recovery

- **Flathub**: Builds are triggered automatically from source when a new release is tagged. If the Flathub listing needs to be re-published, submitting an updated manifest to the Flathub repository is sufficient.
- **GitHub Releases**: Release artifacts are generated via GitHub Actions workflows and can be re-triggered from any tagged release.

### No Server Infrastructure

Aerion is a local desktop application with no server-side components, cloud infrastructure, or databases to restore. All user data (email, contacts, settings, credentials) is stored locally on the user's machine. In the event of data loss:

- **Email**: Re-synced from the user's IMAP server automatically on next launch
- **Credentials**: Re-entered by the user (passwords) or re-authorized (OAuth2 flow)
- **Settings and state**: Regenerated with defaults on first launch
- **Contacts**: Re-synced from CardDAV or IMAP (if configured)

---

## 16. Configuration Integrity

Aerion is a single-user desktop application. The user is the sole administrator and has full control over all configuration.

### Configuration Storage

All security-relevant configuration is stored in a local SQLite database with restricted access:

- **Database file**: 0600 permissions (owner read/write only)
- **Data directories**: 0700 permissions (owner-only access)
- **SQLite WAL mode**: Provides built-in integrity checking with checksums on all writes

### Tamper Detection

- **OS file permissions**: The database and data directories are restricted to the owner. Any modification by another user or process would require privilege escalation.
- **Single-instance lock**: Only one Aerion process can access the database at a time, preventing concurrent modification by unauthorized processes.
- **OS keyring**: Credentials and private keys stored in the OS keyring are protected by the operating system's own integrity and access controls (user session scope, encrypted storage).
- **Schema migrations**: The database schema is versioned. Aerion validates the schema version on startup and applies migrations sequentially — an unexpected schema state would indicate tampering.

### No Remote Configuration

There is no remote configuration server, admin panel, or API that could be used to modify settings externally. All configuration changes occur through the local application UI, mediated by the Go backend.

---

## 17. Debug Mode Disabled in Production

### Default State

Debug mode is **disabled by default** in Aerion. It must be explicitly opted into by the user via:

- `--debug` command-line flag
- `AERION_DEBUG=1` environment variable

Neither of these is set in production builds or Flatpak packages.

### Development vs. Production Builds

- **Development** (`make dev`): Runs with Wails dev server, hot reload, and browser DevTools enabled. Used only during local development.
- **Production** (`make build`, `make flatpak`): Built without dev server or DevTools. The Wails WebView does not expose developer consoles or debug endpoints in production builds.

### Debug Output Safety

Even when debug mode is explicitly enabled, log output does not contain sensitive information such as passwords, OAuth tokens, private keys, or email body content. Debug logging is limited to operational information (sync progress, connection states, error messages).

### No Debug Endpoints

Aerion does not expose any HTTP debug endpoints, status pages, profiling routes, or developer consoles. There is no web server — the application is a local desktop binary with an embedded WebView.

---

## 18. Origin Header Not Used for Access Control

Aerion does not use the `Origin` header (or any HTTP header) for authentication or access control decisions.

- **No HTTP server**: Aerion does not run a web server or expose HTTP endpoints. There are no incoming HTTP requests to inspect headers on.
- **Frontend ↔ Backend**: Communication uses Wails in-process RPC (direct function calls within the same process), not HTTP. No HTTP headers are involved.
- **OAuth2 callback**: The local callback server validates requests using a cryptographic state parameter (PKCE), not the Origin header.
- **IPC**: Detached composer authentication uses a backend-generated token, not HTTP headers.
- **External API calls**: Outbound requests to Google, Microsoft, CardDAV, and key servers authenticate via `Authorization` headers with bearer tokens or credentials — Origin is neither sent nor relied upon.

---

## 19. Cookie-Based Session Tokens

Aerion does not use cookies or cookie-based sessions. There is no HTTP server, no session management layer, and no cookies to protect.

- **No cookies**: The application does not set, read, or transmit cookies of any kind.
- **No sessions**: There is no session token mechanism. The user is authenticated implicitly as the OS-level owner of the application process.
- **Frontend ↔ Backend**: Communication uses Wails in-process RPC — no HTTP, no cookies, no session headers.
- **CSRF not applicable**: Cross-site request forgery requires a web browser making cross-origin requests with attached cookies. Aerion's WebView renders only embedded application assets and does not navigate to external sites. There is no attack vector for CSRF.

---

## 20. LDAP Injection

Aerion does not use LDAP in any capacity. There are no LDAP queries, connections, directory binds, or search filters anywhere in the codebase.

- **Authentication**: Handled via IMAP/SMTP credentials (password or OAuth2 XOAUTH2 SASL) — no LDAP directory authentication.
- **Contact lookup**: Uses CardDAV (WebDAV over HTTPS) and Google/Microsoft REST APIs — not LDAP.
- **No directory services**: Aerion has no integration with Active Directory, OpenLDAP, or any other LDAP-based directory.

LDAP injection is not applicable to this application.

---

## 21. Local and Remote File Inclusion

Aerion is protected against both Local File Inclusion (LFI) and Remote File Inclusion (RFI) attacks.

- **No dynamic file includes**: The application does not include, require, or load files based on user-supplied input. There are no template engines, server-side includes, or dynamic file path resolution that could be exploited.
- **Embedded frontend assets**: All frontend files (HTML, CSS, JavaScript) are compiled into the Go binary at build time via `//go:embed`. They are served from an in-memory filesystem, not from disk paths that could be manipulated.
- **No remote code loading**: The application does not fetch or execute code from remote URLs at runtime (see [Section 6](#6-integrity-protections)).
- **Email HTML sanitization**: Inbound email HTML is sanitized by bluemonday, which strips `<script>`, `<iframe>`, `<object>`, `<embed>`, and other elements that could reference local or remote files for inclusion.
- **Attachment handling**: Attachments are fetched from IMAP servers via the Go backend, not loaded from user-supplied file paths. User-initiated downloads write to a user-chosen location via the OS file picker — the application does not read from arbitrary file paths provided by external input.

---

## 22. Encrypted Storage of Private Data

Aerion stores all data locally on the user's machine. There is no cloud backend, no server-side storage, and no third-party data sharing. Regulated private data is encrypted at rest as follows:

### Encrypted at Rest

| Data | Encryption Method |
|---|---|
| Account passwords | OS keyring (encrypted by OS) or XChaCha20-Poly1305 encrypted DB fallback |
| OAuth2 tokens (access + refresh) | OS keyring or XChaCha20-Poly1305 encrypted DB fallback |
| S/MIME private keys (when enabled) | OS keyring or XChaCha20-Poly1305 encrypted DB fallback |
| PGP private keys (when enabled) | OS keyring or XChaCha20-Poly1305 encrypted DB fallback |
| Draft message bodies (when S/MIME or PGP enabled) | Encrypted to self (CMS envelope or PGP/MIME) before database storage |

### Access-Restricted at Rest

| Data | Protection |
|---|---|
| SQLite database (email messages, contacts, account config) | File permissions 0600 (owner read/write only) |
| Data directories | Directory permissions 0700 (owner-only access) |
| IPC socket | File permissions 0600 |

### Privacy by Design

- **No telemetry or analytics**: Aerion collects no usage data whatsoever.
- **No cloud sync**: All data remains on the user's local machine.
- **No third-party data sharing**: No data is transmitted to any party other than the user's configured mail/contact/key servers and OAuth providers.
- **User-controlled data lifecycle**: Users can delete accounts, messages, contacts, and all associated data through the application UI.
- **Privacy Policy**: Published at [`docs/PRIVACY.md`](https://github.com/hkdb/aerion/blob/main/docs/PRIVACY.md).

---

## 23. Constant-Time Cryptographic Operations

Aerion does not implement any custom cryptographic primitives or algorithms. All cryptographic operations are delegated to established, audited libraries that implement constant-time operations internally:

| Operation | Library | Constant-Time Guarantees |
|---|---|---|
| TLS (IMAP/SMTP/HTTPS) | Go `crypto/tls` | Go standard library uses `crypto/subtle` for constant-time comparisons |
| S/MIME signing/verification | `go.mozilla.org/pkcs7` | Built on Go `crypto/x509`, `crypto/rsa`, `crypto/ecdsa` |
| S/MIME encryption/decryption | `go.mozilla.org/pkcs7` | Same as above |
| PKCS#12 import | `software.sslmate.com/src/go-pkcs12` | Built on Go standard crypto primitives |
| PGP signing/verification | `github.com/ProtonMail/go-crypto` | ProtonMail's audited OpenPGP implementation |
| PGP encryption/decryption | `github.com/ProtonMail/go-crypto` | Same as above |
| Credential encryption (fallback) | `golang.org/x/crypto` (XChaCha20-Poly1305) | Go extended crypto library, constant-time by design |
| OAuth2 PKCE / state validation | Go `crypto/rand`, `crypto/sha256` | Standard library constant-time primitives |

Application-level security comparisons (e.g., IPC token validation) use `crypto/subtle.ConstantTimeCompare`. All other cryptographic operations are handled by the libraries listed above. There are no short-circuit comparisons on secrets, tokens, or cryptographic material in the Aerion codebase.
