package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// microsoftGraphBase is the Graph API root. Overridden in tests via the
// apiBase field on MicrosoftContactsWriter.
const microsoftGraphBase = "https://graph.microsoft.com/v1.0"

// MicrosoftContactsWriter is the Phase 2b.3 Track C write-side client for
// Microsoft Graph contacts. Mirrors GoogleContactsWriter's shape: built fresh
// per call by the extension's api.go using an *http.Client provided by the
// auth broker; tokens never visible to extension code.
//
// Decoupled from internal/contact/microsoft_sync.go on purpose — the READ
// path's parser is search-focused and doesn't model the full Contact field
// set the write path needs (addresses, multiple phone buckets, IM addresses,
// categories). Re-implementing the write shape here keeps the extension
// boundary clean.
type MicrosoftContactsWriter struct {
	httpClient *http.Client
	log        zerolog.Logger
	// apiBase overrides microsoftGraphBase for tests.
	apiBase string
}

// NewMicrosoftContactsWriter constructs a writer with the given authenticated
// HTTP client. Pass an *http.Client from coreapi.Auth().HTTPClient(...) — its
// transport injects the bearer token + refreshes on 401 transparently.
func NewMicrosoftContactsWriter(httpClient *http.Client) *MicrosoftContactsWriter {
	return &MicrosoftContactsWriter{
		httpClient: httpClient,
		log:        logging.WithComponent("microsoft-contacts-write"),
		apiBase:    microsoftGraphBase,
	}
}

// ----- API shapes ------------------------------------------------------------

// msContact is the write-shape for the Graph Contact resource. Fields outside
// what contact.Record models are omitted. Note Graph's per-bucket phone
// arrays (businessPhones / homePhones) vs Google's single typed-array list —
// see microsoft_convert.go for the mapping.
type msContact struct {
	ID          string `json:"id,omitempty"`
	ETag        string `json:"@odata.etag,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	GivenName   string `json:"givenName,omitempty"`
	Surname     string `json:"surname,omitempty"`
	NickName    string `json:"nickName,omitempty"`
	CompanyName string `json:"companyName,omitempty"`
	JobTitle    string `json:"jobTitle,omitempty"`
	Department  string `json:"department,omitempty"`

	// EmailAddresses is the array shape Graph uses. Email "type" doesn't
	// exist on Graph contacts — there's a `name` field that we use to round-
	// trip the EmailType (work / home / etc.) as the entry's name.
	EmailAddresses []msEmailAddress `json:"emailAddresses,omitempty"`

	// Phone buckets. Graph routes by FIELD not by per-entry type — businessPhones
	// is a string[] of work numbers, homePhones is a string[] of home numbers,
	// mobilePhone is a single string. Multiple values per bucket are allowed.
	BusinessPhones []string `json:"businessPhones,omitempty"`
	HomePhones     []string `json:"homePhones,omitempty"`
	MobilePhone    string   `json:"mobilePhone,omitempty"`

	// Address fields are typed too — homeAddress / businessAddress /
	// otherAddress. Each is a single object (no array). Multi-address records
	// distribute across these three slots based on the address type; extras
	// past the three slots get dropped with a log warning on write.
	HomeAddress     *msPhysicalAddress `json:"homeAddress,omitempty"`
	BusinessAddress *msPhysicalAddress `json:"businessAddress,omitempty"`
	OtherAddress    *msPhysicalAddress `json:"otherAddress,omitempty"`

	// BusinessHomePage is the single URL field on a Graph Contact. URL
	// collections in contact.Record collapse to this — first URL wins; rest
	// dropped with a log warn (also surfaced via toast at the UI level).
	BusinessHomePage string `json:"businessHomePage,omitempty"`

	// IMAddresses is a string[] (no protocol field). Encoded "protocol:handle"
	// stays as a single string round-trip — converter strips/restores the prefix
	// at the field boundary so the user's protocol metadata is preserved when
	// the record stays inside Aerion's data model.
	IMAddresses []string `json:"imAddresses,omitempty"`

	Categories []string `json:"categories,omitempty"`

	Birthday string `json:"birthday,omitempty"`     // ISO 8601 datetime; we send YYYY-MM-DDTHH:00:00Z
	Personal string `json:"personalNotes,omitempty"`
}

type msEmailAddress struct {
	Address string `json:"address,omitempty"`
	Name    string `json:"name,omitempty"` // we round-trip EmailType here
}

type msPhysicalAddress struct {
	Street          string `json:"street,omitempty"`
	City            string `json:"city,omitempty"`
	State           string `json:"state,omitempty"`
	CountryOrRegion string `json:"countryOrRegion,omitempty"`
	PostalCode      string `json:"postalCode,omitempty"`
}

// msContactFolder is the Graph ContactFolder resource. Used for the addressbook
// picker — each user-created folder appears as a pseudo-addressbook row in the
// Add Contact dialog. The default folder (/me/contacts, no folder routing) is
// represented by a synthetic "Default Contacts" row in the API layer.
type msContactFolder struct {
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	ParentID    string `json:"parentFolderId,omitempty"`
}

type msContactFoldersResponse struct {
	Value    []msContactFolder `json:"value,omitempty"`
	NextLink string            `json:"@odata.nextLink,omitempty"`
}

type msAPIErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ----- HTTP plumbing ---------------------------------------------------------

// doJSON sends a JSON request to Graph with single-retry on 429/503 honoring
// Retry-After. Returns the decoded response body on 2xx, or a wrapped error
// containing Graph's error code + message on failure. Auth broker handles 401
// refresh transparently.
func (w *MicrosoftContactsWriter) doJSON(ctx context.Context, method, urlStr string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("microsoft graph: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
		if err != nil {
			return fmt.Errorf("microsoft graph: build request: %w", err)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")

		resp, err := w.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("microsoft graph: %s %s: %w", method, urlStr, err)
		}

		switch {
		case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent:
			defer resp.Body.Close()
			if out == nil || resp.StatusCode == http.StatusNoContent {
				_, _ = io.Copy(io.Discard, resp.Body)
				return nil
			}
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("microsoft graph: decode response: %w", err)
			}
			return nil

		case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusServiceUnavailable:
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			if attempt == 1 {
				return fmt.Errorf("microsoft graph: rate-limited after retry (status %d)", resp.StatusCode)
			}
			if retryAfter > 0 {
				w.log.Warn().Dur("retry_after", retryAfter).Int("status", resp.StatusCode).Msg("Graph rate-limited; sleeping")
				select {
				case <-time.After(retryAfter):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if body != nil {
				buf, _ := json.Marshal(body)
				bodyReader = bytes.NewReader(buf)
			}
			continue

		default:
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			return classifyMicrosoftError(resp.StatusCode, data)
		}
	}
	return errors.New("microsoft graph: doJSON: unreachable")
}

// doBinary sends a binary PATCH request (used for photo upload). Same retry
// semantics as doJSON. The Graph photo endpoint replies with 200 OK and
// minimal body — we just consume it.
func (w *MicrosoftContactsWriter) doBinary(ctx context.Context, method, urlStr, contentType string, body []byte) error {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, urlStr, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("microsoft graph: build request: %w", err)
		}
		req.Header.Set("Content-Type", contentType)

		resp, err := w.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("microsoft graph: %s %s: %w", method, urlStr, err)
		}

		switch {
		case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent:
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil

		case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusServiceUnavailable:
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			if attempt == 1 {
				return fmt.Errorf("microsoft graph: rate-limited after retry (status %d)", resp.StatusCode)
			}
			if retryAfter > 0 {
				w.log.Warn().Dur("retry_after", retryAfter).Int("status", resp.StatusCode).Msg("Graph rate-limited; sleeping")
				select {
				case <-time.After(retryAfter):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			continue

		default:
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			return classifyMicrosoftError(resp.StatusCode, data)
		}
	}
	return errors.New("microsoft graph: doBinary: unreachable")
}

// classifyMicrosoftError unwraps Graph's standard error envelope into a
// human-readable Go error. No typed conflict error here — Graph contacts
// don't enforce etag, so we don't expect 412 in normal flow.
func classifyMicrosoftError(status int, data []byte) error {
	var body msAPIErrorBody
	_ = json.Unmarshal(data, &body)
	msg := body.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(data))
		if len(msg) > 256 {
			msg = msg[:256] + "..."
		}
	}
	code := body.Error.Code
	switch {
	case msg != "" && code != "":
		return fmt.Errorf("microsoft graph: HTTP %d %s: %s", status, code, msg)
	case msg != "":
		return fmt.Errorf("microsoft graph: HTTP %d: %s", status, msg)
	}
	return fmt.Errorf("microsoft graph: HTTP %d", status)
}

// ----- public methods --------------------------------------------------------

// CreateContact POSTs a new Contact into either the default mailbox folder
// (/me/contacts) when folderID is empty, OR into a specific contactFolder
// (/me/contactFolders/{folderID}/contacts). Returns the server's view, which
// carries the freshly-assigned id and @odata.etag.
func (w *MicrosoftContactsWriter) CreateContact(ctx context.Context, folderID string, contact *msContact) (*msContact, error) {
	if contact == nil {
		return nil, errors.New("microsoft graph: CreateContact: nil contact")
	}
	// Defensive copy: drop server-assigned fields before sending.
	clean := *contact
	clean.ID = ""
	clean.ETag = ""

	target := w.apiBase + "/me/contacts"
	if folderID != "" {
		target = w.apiBase + "/me/contactFolders/" + url.PathEscape(folderID) + "/contacts"
	}
	var out msContact
	if err := w.doJSON(ctx, http.MethodPost, target, &clean, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateContact PATCHes an existing Contact. Graph keys contacts by id within
// the mailbox, not within a folder, so no folder routing is needed on update.
// No If-Match header — Graph contacts don't enforce etag.
//
// Send the FULL contact body even on partial edits — Graph treats missing
// fields as "leave unchanged" for scalars, but multi-value arrays (phones,
// emails, categories) get REPLACED if present. Caller is responsible for
// populating the full intended state.
func (w *MicrosoftContactsWriter) UpdateContact(ctx context.Context, contactID string, contact *msContact) (*msContact, error) {
	if contactID == "" {
		return nil, errors.New("microsoft graph: UpdateContact: contactID is required")
	}
	if contact == nil {
		return nil, errors.New("microsoft graph: UpdateContact: nil contact")
	}
	clean := *contact
	clean.ID = ""
	clean.ETag = ""

	target := w.apiBase + "/me/contacts/" + url.PathEscape(contactID)
	var out msContact
	if err := w.doJSON(ctx, http.MethodPatch, target, &clean, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetContact fetches a single contact. Reserved for future use — Microsoft's
// last-writer-wins design means we don't currently round-trip a fetch before
// edits the way the Google path does. Kept symmetric with the Google writer.
func (w *MicrosoftContactsWriter) GetContact(ctx context.Context, contactID string) (*msContact, error) {
	if contactID == "" {
		return nil, errors.New("microsoft graph: GetContact: contactID is required")
	}
	target := w.apiBase + "/me/contacts/" + url.PathEscape(contactID)
	var out msContact
	if err := w.doJSON(ctx, http.MethodGet, target, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteContact removes a contact server-side. Cascades the contact's
// membership in any folders.
func (w *MicrosoftContactsWriter) DeleteContact(ctx context.Context, contactID string) error {
	if contactID == "" {
		return errors.New("microsoft graph: DeleteContact: contactID is required")
	}
	target := w.apiBase + "/me/contacts/" + url.PathEscape(contactID)
	return w.doJSON(ctx, http.MethodDelete, target, nil, nil)
}

// UpdatePhoto sets the contact's photo from raw JPEG bytes. Graph's photo
// endpoint accepts the binary directly as the request body (not multipart).
// Caller controls bytes — typically the resized JPEG produced by the
// Contacts_ResizeContactPhoto frontend helper.
func (w *MicrosoftContactsWriter) UpdatePhoto(ctx context.Context, contactID string, jpegBytes []byte) error {
	if contactID == "" {
		return errors.New("microsoft graph: UpdatePhoto: contactID is required")
	}
	if len(jpegBytes) == 0 {
		return errors.New("microsoft graph: UpdatePhoto: empty photo bytes")
	}
	target := w.apiBase + "/me/contacts/" + url.PathEscape(contactID) + "/photo/$value"
	return w.doBinary(ctx, http.MethodPatch, target, "image/jpeg", jpegBytes)
}

// ListContactFolders returns the user's contact folders for the Add Contact
// dialog's addressbook picker. Filters happen at the API layer (Graph
// returns ALL folders including system ones; the picker shows them as-is
// since Graph doesn't expose an obvious "system vs user" discriminator the
// way Google does with GroupType — folder structure is user-driven on the
// MS side).
//
// Paginates via @odata.nextLink. Reasonable real-world folder counts mean
// 1-2 pages typically.
func (w *MicrosoftContactsWriter) ListContactFolders(ctx context.Context) ([]msContactFolder, error) {
	target := w.apiBase + "/me/contactFolders"
	var all []msContactFolder
	for target != "" {
		var resp msContactFoldersResponse
		if err := w.doJSON(ctx, http.MethodGet, target, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Value...)
		target = resp.NextLink
	}
	return all, nil
}
