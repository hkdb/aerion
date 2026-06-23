// Package auth implements the Auth Broker — the single path through which
// Aerion extensions reach external services (Google APIs, Microsoft Graph,
// CalDAV/CardDAV servers, IMAP/SMTP).
//
// Extensions never see access tokens, refresh tokens, or passwords. The Broker
// returns a pre-configured *http.Client that injects bearer tokens and
// refreshes them on 401 transparently. For protocol-level access (IMAP, SMTP),
// the Broker returns a connected client with the keyring-managed credentials
// already applied.
//
// Multi-client-config routing: an account can hold OAuth tokens under multiple
// client configurations (e.g., Mail under "google-mail", Calendar under
// "google-extensions"). The Broker resolves the right ClientConfigID for the
// requested scopes and reads/writes tokens under that pair. If the account
// lacks tokens covering the requested scopes, the Broker returns
// ErrAdditionalConsentRequired; the host runs an incremental-consent flow and
// the extension retries.
package auth
