package backend

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/hkdb/aerion/extensions/contacts/backend/imaging"
	"github.com/hkdb/aerion/internal/carddav"
	"github.com/hkdb/aerion/internal/contact"
	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// Source IDs for Aerion's core local contact store. CardDAV sources use their
// own UUIDs (one per configured source).
//
// The local source has two sub-categories distinguished by `contacts.kind`:
//   - manual    → entries the user added via the Add Contact UI
//   - collected → auto-collected from sent-mail recipients
//
// The "Local" parent (SourceIDLocal) returns both. Sub-source values select
// one kind only.
const (
	SourceIDLocal          = "local"
	SourceIDLocalManual    = "local:manual"
	SourceIDLocalCollected = "local:collected"
)

// isLocalSource reports whether the given filter sourceID targets the local
// store (either the parent "local" or a sub-source like "local:manual").
func isLocalSource(id string) bool {
	return id == SourceIDLocal || strings.HasPrefix(id, SourceIDLocal+":")
}

// localKindFromSourceID returns the `contacts.kind` filter value for a local
// sub-source ID, or "" for the parent "local" (= no filter, return both).
func localKindFromSourceID(id string) string {
	switch id {
	case SourceIDLocalManual:
		return "manual"
	case SourceIDLocalCollected:
		return "collected"
	default:
		return ""
	}
}

// API implements coreapi.Contacts by wrapping the existing core contact.Store
// and carddav.Store. getCardDAVPassword is a host-provided closure that
// resolves per-source basic-auth passwords for CardDAV writes (Phase
// 2b.2.b.1); it may be nil in test fixtures that never exercise CardDAV
// writes. The closure pattern (rather than a direct *credentials.Store)
// keeps this package free of `internal/credentials` so the extension
// conforms to the docs/EXTENSIONS.md "no internal imports" rule.
type API struct {
	localStore         *contact.Store
	carddavStore       *carddav.Store
	extStore           *Store       // per-extension SQLite; backs oauth_record_state for Phase 2b.3 write paths
	core               coreapi.Core // Phase 2b.3: handle on coreapi.Auth for OAuth provider write paths; nil disables Google/MS writes
	// db is the shared application DB. Phase 2b.3 OAuth write paths use this
	// to compose contact.UpsertRecordTx + carddav_record_state writes inside
	// a single transaction. Nil disables OAuth provider writes (tests).
	db                 *sql.DB
	getCardDAVPassword CardDAVPasswordFunc
}

// NewAPI constructs the Contacts API wrapper. Any store may be nil — the
// wrapper degrades gracefully (a profile with no CardDAV sources has nil
// carddavStore; search still returns local results; CardDAV writes refuse with
// a clear error rather than panicking). extStore is the per-extension SQLite
// wrapper; nil means OAuth ETag tracking is disabled (tests typically pass
// nil since they don't exercise Google/MS write paths).
//
// core is the coreapi.Core handle used by the OAuth write paths (Phase 2b.3) to
// reach the auth broker via core.Auth().HTTPClient. Nil means OAuth provider
// writes return a clear "core not wired" error rather than panicking — tests
// that don't exercise those paths pass nil.
//
// db is the shared application DB; used by OAuth provider write paths to
// compose contact.UpsertRecordTx + carddav_record_state inserts in a single
// transaction. Nil-safe in the same way as core.
func NewAPI(localStore *contact.Store, carddavStore *carddav.Store, extStore *Store, core coreapi.Core, db *sql.DB, getCardDAVPassword CardDAVPasswordFunc) *API {
	return &API{
		localStore:         localStore,
		carddavStore:       carddavStore,
		extStore:           extStore,
		core:               core,
		db:                 db,
		getCardDAVPassword: getCardDAVPassword,
	}
}

// SearchContacts delegates to the core contact store's merged search across
// local, vCard, and CardDAV sources. The query is matched against email and
// display name; ranking is by send count + recency + source priority.
func (a *API) SearchContacts(query string, limit int) ([]coreapi.Contact, error) {
	if a.localStore == nil {
		return nil, nil
	}
	results, err := a.localStore.Search(query, limit)
	if err != nil {
		return nil, fmt.Errorf("contacts.SearchContacts: %w", err)
	}
	out := make([]coreapi.Contact, 0, len(results))
	for _, c := range results {
		out = append(out, fromLocal(c))
	}
	return out, nil
}

// GetContact looks up a contact by email (if the argument contains '@') or by
// CardDAV UUID otherwise. Returns (nil, nil) when not found.
//
// For email-shaped IDs the lookup tries the local store first. When the local
// store misses (common case for the "All" view where a CardDAV contact's row
// surfaces with its email as the synthetic ID — the merged-search bridge
// drops the CardDAV UUID), the method falls back to the CardDAV store keyed
// by email. This keeps the detail pane populated for CardDAV-only contacts
// without requiring the merge bridge to round-trip the UUID through
// contact.Contact (which has no ID field).
func (a *API) GetContact(emailOrID string) (*coreapi.Contact, error) {
	if emailOrID == "" {
		return nil, nil
	}
	if a.localStore == nil {
		return nil, nil
	}

	// Try as record_id first. Local records have IDs like "local-<email>" and
	// CardDAV records have UUIDs — both work as record IDs. This handles the
	// majority case where the caller already has the canonical id.
	rec, err := a.localStore.GetRecord(emailOrID)
	if err != nil {
		return nil, fmt.Errorf("contacts.GetContact: %w", err)
	}
	if rec != nil {
		out := fromRecord(rec)
		a.enrichCardDAVSourceID(&out, rec)
		return &out, nil
	}

	// Fall back to email lookup. Used when a caller passes a bare email
	// (e.g., from autocomplete results whose ID is the email rather than the
	// canonical record_id).
	if strings.Contains(emailOrID, "@") {
		rec, err := a.localStore.GetRecordByEmail(emailOrID)
		if err != nil {
			return nil, fmt.Errorf("contacts.GetContact: %w", err)
		}
		if rec != nil {
			out := fromRecord(rec)
			a.enrichCardDAVSourceID(&out, rec)
			return &out, nil
		}
	}
	return nil, nil
}

// enrichCardDAVSourceID rewrites coreapi.Contact.SourceID from the literal
// "carddav" string that fromRecord defaults to into the actual CardDAV source
// UUID, so the frontend's writability gate (which queries
// contactSourcesStore.isSourceWritable(sourceId)) can find the row. No-op when
// the record isn't a CardDAV record or when the source lookup misses.
func (a *API) enrichCardDAVSourceID(out *coreapi.Contact, rec *contact.Record) {
	if out == nil || rec == nil || rec.Source != "carddav" || a.carddavStore == nil {
		return
	}
	sourceID, err := a.carddavStore.GetSourceIDForRecord(rec.ID)
	if err != nil || sourceID == "" {
		return
	}
	out.SourceID = sourceID
}

// ListContacts returns contacts filtered by SourceID:
//   - ""                       → merged search across all sources (uses Query if set)
//   - SourceIDLocal            → all local contacts (manual + collected)
//   - SourceIDLocalManual      → user-added local contacts only
//   - SourceIDLocalCollected   → auto-collected local contacts only
//   - <carddav uuid>           → a specific CardDAV source, paged via offset/limit
func (a *API) ListContacts(filter coreapi.ContactFilter) ([]coreapi.Contact, error) {
	switch {
	case filter.SourceID == "":
		return a.listMerged(filter)
	case isLocalSource(filter.SourceID):
		return a.listLocal(filter)
	default:
		return a.listCardDAV(filter)
	}
}

// CreateContact creates a new contact in the source identified by input.SourceID.
// Source dispatch:
//
//   - "", "local", "local:manual" → local manual entry via contact.Store.Create
//     (kind='manual', name_overridden=1, send_count=0). Returns the email as
//     the new contact's id. Errors with ErrContactExists when the email is
//     already present.
//   - "local:collected"            → REJECTED. The 'collected' kind is reserved
//     for the sent-mail collection process to assign.
//   - <CardDAV source UUID>        → PUTs a new vCard to the source's addressbook
//     identified by input.AddressbookID (or the source's first enabled+writable
//     addressbook when AddressbookID is ""). Returns the new record UUID.
//   - Google / Microsoft           → returns ErrUnimplemented. 2b.3.
//
// Email is normalized (trim + lowercase) before storage. For local contacts
// the returned id is the normalized email; for CardDAV contacts it's the
// generated record UUID.
func (a *API) CreateContact(input coreapi.ContactCreateInput) (string, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if email == "" {
		return "", fmt.Errorf("contacts.CreateContact: email is required")
	}
	if !strings.Contains(email, "@") {
		return "", fmt.Errorf("contacts.CreateContact: email is not valid")
	}

	switch {
	case input.SourceID == "" || input.SourceID == SourceIDLocal || input.SourceID == SourceIDLocalManual:
		if a.localStore == nil {
			return "", fmt.Errorf("contacts.CreateContact: local store unavailable")
		}
		if err := a.localStore.Create(email, strings.TrimSpace(input.Name)); err != nil {
			return "", err
		}
		return email, nil
	case input.SourceID == SourceIDLocalCollected:
		return "", fmt.Errorf("contacts.CreateContact: cannot manually create a Collected contact (auto-derived from sent mail)")
	}

	// External-source dispatch by carddav.Source.Type (Phase 2b.3).
	if a.carddavStore == nil {
		return "", fmt.Errorf("contacts.CreateContact: carddav store unavailable")
	}
	source, err := a.carddavStore.GetSource(input.SourceID)
	if err != nil {
		return "", fmt.Errorf("contacts.CreateContact: lookup source %s: %w", input.SourceID, err)
	}
	if source == nil {
		return "", fmt.Errorf("contacts.CreateContact: source %s not found", input.SourceID)
	}
	switch source.Type {
	case carddav.SourceTypeCardDAV:
		return a.createCardDAVContact(input, email)
	case carddav.SourceTypeGoogle:
		return a.createGoogleContact(input, email, source)
	case carddav.SourceTypeMicrosoft:
		return a.createMicrosoftContact(input, email, source)
	}
	return "", fmt.Errorf("contacts.CreateContact: unknown source type %q for source %s", source.Type, input.SourceID)
}

// createGoogleContact lives in google_api.go (Phase 2b.3 Track B).
// createMicrosoftContact lives in microsoft_api.go (Phase 2b.3 Track C).

// createCardDAVContact resolves the target addressbook + client and PUTs a new
// vCard. Used by CreateContact when the SourceID is a CardDAV source UUID.
// The record's UUID is freshly generated; href is "<addressbookPath><uuid>.vcf".
//
// Caller (CreateContact dispatch) has already validated the source exists and
// is a CardDAV source — this function trusts that and skips the redundant
// lookup. The source.Type guard lives in CreateContact's switch.
func (a *API) createCardDAVContact(input coreapi.ContactCreateInput, email string) (string, error) {
	// Resolve the target addressbook. If the caller passed one, validate it
	// belongs to this source. Otherwise pick the first enabled addressbook.
	addressbookID := input.AddressbookID
	if addressbookID != "" {
		ab, err := a.carddavStore.GetAddressbook(addressbookID)
		if err != nil {
			return "", fmt.Errorf("contacts.CreateContact: lookup addressbook %s: %w", addressbookID, err)
		}
		if ab == nil {
			return "", fmt.Errorf("contacts.CreateContact: addressbook %s not found", addressbookID)
		}
		if ab.SourceID != input.SourceID {
			return "", fmt.Errorf("contacts.CreateContact: addressbook %s does not belong to source %s", addressbookID, input.SourceID)
		}
	}
	if addressbookID == "" {
		abs, err := a.carddavStore.ListAddressbooks(input.SourceID)
		if err != nil {
			return "", fmt.Errorf("contacts.CreateContact: list addressbooks for %s: %w", input.SourceID, err)
		}
		for _, ab := range abs {
			if ab != nil && ab.Enabled {
				addressbookID = ab.ID
				break
			}
		}
		if addressbookID == "" {
			return "", fmt.Errorf("contacts.CreateContact: source %s has no enabled addressbook to write to", input.SourceID)
		}
	}

	// Build the record. FN is required by vCard 3.0 — fall back to email
	// when Name is blank.
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = email
	}
	rec := &contact.Record{
		Fn: name,
		Emails: []contact.RecordEmail{
			{
				Email:     email,
				EmailType: "",
				IsPrimary: true,
			},
		},
	}

	client, _, err := a.cardDAVClientForAddressbook(addressbookID)
	if err != nil {
		return "", fmt.Errorf("contacts.CreateContact: %w", err)
	}

	id, err := a.carddavStore.CreateRecord(addressbookID, rec, client)
	if err != nil {
		var pre *carddav.ErrPreconditionFailed
		if errors.As(err, &pre) {
			return "", &coreapi.ErrConflict{ContactID: "", Message: "a contact already exists at the generated href; please retry"}
		}
		return "", fmt.Errorf("contacts.CreateContact (carddav source %s): %w", input.SourceID, err)
	}
	return id, nil
}

// UpdateContact mutates a contact by id. Source dispatch:
//
//   - Local (record.Source == "local"): updates display_name via
//     contact.Store.UpdateRecordName, which also sets name_overridden=1 so
//     future AddOrUpdate calls on sent mail won't clobber the user edit.
//   - CardDAV (record.Source == "carddav"): PUTs the full record to the
//     CardDAV server gated on the source's writable flag, then mirrors the
//     server's accepted state locally. 412 conflicts surface as
//     *coreapi.ErrConflict after refreshing the local cache.
//   - Google / Microsoft (other source values): returns ErrUnimplemented;
//     filled in by Phase 2b.3 via the Auth Broker.
//
// Phase 2b.2.b.1 ships only the Name field in ContactPatch; other patch
// fields land in 2b.2.b.2 alongside the multi-field Edit dialog. The CardDAV
// write path is full-fidelity already (UpdateRecord serializes the entire
// record's current state), so 2b.2.b.2's UI is purely additive frontend work.
//
// Empty/nil patch (no fields set) is a no-op success — callers can issue a
// "touch" call without sending field updates.
func (a *API) UpdateContact(id string, patch coreapi.ContactPatch) error {
	if id == "" {
		return fmt.Errorf("contacts.UpdateContact: id is required")
	}
	if a.localStore == nil {
		return fmt.Errorf("contacts.UpdateContact: local store unavailable")
	}

	// Resolve the id to a record. Try as record_id first (works for both the
	// "local-<email>" form and CardDAV UUIDs); fall back to email lookup if
	// the caller passed a bare email.
	rec, err := a.localStore.GetRecord(id)
	if err != nil {
		return fmt.Errorf("contacts.UpdateContact: %w", err)
	}
	if rec == nil && strings.Contains(id, "@") {
		rec, err = a.localStore.GetRecordByEmail(id)
		if err != nil {
			return fmt.Errorf("contacts.UpdateContact: %w", err)
		}
	}
	if rec == nil {
		// No matching record. Idempotent miss.
		return nil
	}

	// Apply every non-nil patch field to the resolved record. After this,
	// `rec` carries the full intended state — the source-dispatch below
	// just needs to persist it.
	if !applyContactPatchToRecord(rec, patch) {
		// All patch fields nil — no-op success.
		return nil
	}

	// Local-source records: full-fidelity write via UpsertRecord (which wraps
	// UpsertRecordTx in a transaction). Replaces all sub-table rows wholesale.
	if rec.Source == "local" {
		return a.localStore.UpsertRecord(rec)
	}

	// External-source records (rec.Source == "carddav" in contact_records for
	// CardDAV, Google, AND Microsoft — they all share that row's source tag).
	// Distinguish by the carddav.Source.Type one level up via the addressbook
	// linkage. Phase 2b.3 splits this into per-provider write paths.
	if rec.Source == "carddav" {
		sourceType, err := a.sourceTypeForRecord(rec)
		if err != nil {
			return err
		}
		switch sourceType {
		case carddav.SourceTypeCardDAV:
			return a.writeCardDAVRecord(rec)
		case carddav.SourceTypeGoogle:
			return a.updateGoogleContact(rec)
		case carddav.SourceTypeMicrosoft:
			return a.updateMicrosoftContact(rec)
		}
	}
	return coreapi.ErrUnimplemented
}

// updateGoogleContact lives in google_api.go. updateMicrosoftContact lives
// in microsoft_api.go (Phase 2b.3 Track C).

// applyContactPatchToRecord copies every non-nil patch field onto the record.
// Returns true if any field was applied; false if the patch was entirely nil
// (caller can short-circuit with a no-op success).
//
// Scalar fields are simple string assignments (with TrimSpace for user input).
// Multi-value fields use the pointer-to-slice contract: non-nil empty slice
// = clear, non-nil populated slice = replace. Photo uses pointer-to-struct
// with the same semantics: non-nil with empty Data+URL = clear.
func applyContactPatchToRecord(rec *contact.Record, patch coreapi.ContactPatch) bool {
	applied := false
	if patch.Name != nil {
		rec.Fn = strings.TrimSpace(*patch.Name)
		// Mark all emails as name_overridden so future AddOrUpdate calls from
		// sent-mail auto-collection don't clobber the user edit. Matches the
		// behavior the legacy UpdateRecordName path had.
		for i := range rec.Emails {
			rec.Emails[i].NameOverridden = true
		}
		applied = true
	}
	if patch.Nickname != nil {
		rec.Nickname = strings.TrimSpace(*patch.Nickname)
		applied = true
	}
	if patch.Org != nil {
		rec.Org = strings.TrimSpace(*patch.Org)
		applied = true
	}
	if patch.Title != nil {
		rec.Title = strings.TrimSpace(*patch.Title)
		applied = true
	}
	if patch.Note != nil {
		rec.Note = strings.TrimSpace(*patch.Note)
		applied = true
	}
	if patch.Bday != nil {
		rec.Bday = strings.TrimSpace(*patch.Bday)
		applied = true
	}
	if patch.Emails != nil {
		rec.Emails = nil
		for _, e := range *patch.Emails {
			rec.Emails = append(rec.Emails, contact.RecordEmail{
				Email:     strings.ToLower(strings.TrimSpace(e.Email)),
				EmailType: e.Type,
				IsPrimary: e.IsPrimary,
			})
		}
		applied = true
	}
	if patch.Phones != nil {
		rec.Phones = nil
		for _, p := range *patch.Phones {
			rec.Phones = append(rec.Phones, contact.RecordPhone{
				Number:    strings.TrimSpace(p.Number),
				PhoneType: p.Type,
				IsPrimary: p.IsPrimary,
			})
		}
		applied = true
	}
	if patch.Addresses != nil {
		rec.Addresses = nil
		for _, a := range *patch.Addresses {
			rec.Addresses = append(rec.Addresses, contact.RecordAddress{
				AddrType: a.Type,
				Street:   strings.TrimSpace(a.Street),
				City:     strings.TrimSpace(a.City),
				Region:   strings.TrimSpace(a.Region),
				Postcode: strings.TrimSpace(a.Postcode),
				Country:  strings.TrimSpace(a.Country),
			})
		}
		applied = true
	}
	if patch.URLs != nil {
		rec.URLs = nil
		for _, u := range *patch.URLs {
			rec.URLs = append(rec.URLs, contact.RecordURL{
				URL:     strings.TrimSpace(u.URL),
				URLType: u.Type,
			})
		}
		applied = true
	}
	if patch.IMPPs != nil {
		rec.IMPPs = nil
		for _, i := range *patch.IMPPs {
			rec.IMPPs = append(rec.IMPPs, contact.RecordIMPP{
				Handle:   strings.TrimSpace(i.Handle),
				IMPPType: i.Type,
			})
		}
		applied = true
	}
	if patch.Categories != nil {
		rec.Categories = append([]string{}, *patch.Categories...)
		applied = true
	}
	if patch.Photo != nil {
		rec.PhotoData = strings.TrimSpace(patch.Photo.Data)
		rec.PhotoMediaType = strings.TrimSpace(patch.Photo.MediaType)
		rec.PhotoURL = strings.TrimSpace(patch.Photo.URL)
		applied = true
	}
	return applied
}

// DeleteContact removes a contact by id. Source dispatch:
//
//   - Local: cascade-deletes the record (and its sub-tables) via
//     contact.Store.DeleteRecord.
//   - CardDAV: DELETEs the resource from the server (gated on the source's
//     writable flag), then cascade-deletes locally. 412 conflicts surface as
//     *coreapi.ErrConflict after refreshing the local cache.
//   - Google / Microsoft: returns ErrUnimplemented until Phase 2b.3.
//
// Idempotent on the local + 404 paths (deleting a non-existent contact
// succeeds).
func (a *API) DeleteContact(id string) error {
	if id == "" {
		return fmt.Errorf("contacts.DeleteContact: id is required")
	}
	if a.localStore == nil {
		return fmt.Errorf("contacts.DeleteContact: local store unavailable")
	}

	// Resolve to a record. Try record_id first (works for both "local-<email>"
	// and CardDAV UUIDs); fall back to email lookup if the caller passed a bare
	// email.
	rec, err := a.localStore.GetRecord(id)
	if err != nil {
		return fmt.Errorf("contacts.DeleteContact: %w", err)
	}
	if rec == nil && strings.Contains(id, "@") {
		rec, err = a.localStore.GetRecordByEmail(id)
		if err != nil {
			return fmt.Errorf("contacts.DeleteContact: %w", err)
		}
	}
	if rec == nil {
		// Idempotent miss.
		return nil
	}

	// Local records: cascade-delete via the record id.
	if rec.Source == "local" {
		return a.localStore.DeleteRecord(rec.ID)
	}

	// External-source records: dispatch by carddav.Source.Type. See UpdateContact
	// for the same pattern (contact_records.source == 'carddav' for all three;
	// the source type is one level up).
	if rec.Source == "carddav" {
		sourceType, err := a.sourceTypeForRecord(rec)
		if err != nil {
			return err
		}
		switch sourceType {
		case carddav.SourceTypeCardDAV:
			return a.deleteCardDAVRecord(rec)
		case carddav.SourceTypeGoogle:
			return a.deleteGoogleContact(rec)
		case carddav.SourceTypeMicrosoft:
			return a.deleteMicrosoftContact(rec)
		}
	}
	return coreapi.ErrUnimplemented
}

// deleteGoogleContact lives in google_api.go. deleteMicrosoftContact lives
// in microsoft_api.go (Phase 2b.3 Track C).

// sourceTypeForRecord looks up the carddav.SourceType for a record by walking
// rec.SourceRef (its addressbook id) → carddav_source_addressbooks → source.
// Used by the external-source dispatch in UpdateContact and DeleteContact to
// route between CardDAV, Google, and Microsoft write paths.
//
// All three source types tag contact_records.source as 'carddav' (the column
// just distinguishes "local" vs "external"), so the actual provider lives on
// the source row two joins away.
func (a *API) sourceTypeForRecord(rec *contact.Record) (carddav.SourceType, error) {
	if a.carddavStore == nil {
		return "", fmt.Errorf("sourceTypeForRecord: carddav store unavailable")
	}
	if rec == nil {
		return "", fmt.Errorf("sourceTypeForRecord: nil record")
	}
	if rec.SourceRef == "" {
		return "", fmt.Errorf("sourceTypeForRecord: record has no addressbook reference")
	}
	// Direct lookup — don't go through cardDAVClientForRecord because that
	// gates on Writable + fetches credentials (irrelevant for type lookup, and
	// would early-error for non-writable OAuth sources before we can route).
	source, err := a.carddavStore.GetSourceForAddressbook(rec.SourceRef)
	if err != nil {
		return "", fmt.Errorf("sourceTypeForRecord: lookup source for addressbook %s: %w", rec.SourceRef, err)
	}
	if source == nil {
		return "", fmt.Errorf("sourceTypeForRecord: no source owns addressbook %s", rec.SourceRef)
	}
	return source.Type, nil
}

// writeCardDAVRecord is the shared CardDAV-write dispatch used by UpdateContact.
// Resolves the source for the record's addressbook, checks the writable flag,
// builds an authenticated client with the source's basic-auth creds, then
// delegates the PUT + local sync to carddav.Store.UpdateRecord.
//
// On a 412 conflict, refreshes the local cache from the server and returns a
// *coreapi.ErrConflict the Wails layer translates into a contacts:conflict
// event.
func (a *API) writeCardDAVRecord(rec *contact.Record) error {
	client, sourceID, err := a.cardDAVClientForRecord(rec)
	if err != nil {
		return err
	}
	if err := a.carddavStore.UpdateRecord(rec, client); err != nil {
		var pre *carddav.ErrPreconditionFailed
		if errors.As(err, &pre) {
			_ = a.carddavStore.RefreshRecordFromServer(rec.ID, client)
			return &coreapi.ErrConflict{ContactID: rec.ID, Message: "the contact was modified on the server"}
		}
		return fmt.Errorf("contacts.UpdateContact (carddav source %s): %w", sourceID, err)
	}
	return nil
}

// deleteCardDAVRecord is the shared CardDAV-delete dispatch. Same recipe as
// writeCardDAVRecord with DELETE in place of PUT.
func (a *API) deleteCardDAVRecord(rec *contact.Record) error {
	client, sourceID, err := a.cardDAVClientForRecord(rec)
	if err != nil {
		return err
	}
	if err := a.carddavStore.DeleteRecord(rec.ID, client); err != nil {
		var pre *carddav.ErrPreconditionFailed
		if errors.As(err, &pre) {
			_ = a.carddavStore.RefreshRecordFromServer(rec.ID, client)
			return &coreapi.ErrConflict{ContactID: rec.ID, Message: "the contact was modified on the server"}
		}
		return fmt.Errorf("contacts.DeleteContact (carddav source %s): %w", sourceID, err)
	}
	return nil
}

// cardDAVClientForRecord resolves the source for an EXISTING CardDAV record
// (via the record's source_ref = addressbook_id) and delegates to
// cardDAVClientForAddressbook. Used by UpdateContact / DeleteContact paths
// which already have a record loaded.
func (a *API) cardDAVClientForRecord(rec *contact.Record) (*carddav.Client, string, error) {
	if rec == nil {
		return nil, "", fmt.Errorf("nil record")
	}
	if rec.SourceRef == "" {
		return nil, "", fmt.Errorf("record has no addressbook reference")
	}
	return a.cardDAVClientForAddressbook(rec.SourceRef)
}

// cardDAVClientForAddressbook resolves the source for an addressbook, gates on
// the source's writable flag, fetches the basic-auth password from the
// credentials store, and returns a ready-to-use Client. Used by CreateContact
// (where the record doesn't yet have a source_ref) and via
// cardDAVClientForRecord for update/delete paths.
func (a *API) cardDAVClientForAddressbook(addressbookID string) (*carddav.Client, string, error) {
	if a.carddavStore == nil {
		return nil, "", fmt.Errorf("carddav store unavailable")
	}
	source, err := a.carddavStore.GetSourceForAddressbook(addressbookID)
	if err != nil {
		return nil, "", fmt.Errorf("lookup source for addressbook %s: %w", addressbookID, err)
	}
	if source == nil {
		return nil, "", fmt.Errorf("no source owns addressbook %s", addressbookID)
	}
	// Writability is a user-facing permission gate — fire it before the
	// credentials-store check so non-writable sources surface the right
	// error regardless of whether credentials are loadable.
	if !source.Writable {
		return nil, source.ID, fmt.Errorf("this source is not writable; enable write access in its settings")
	}
	if a.getCardDAVPassword == nil {
		return nil, source.ID, fmt.Errorf("credentials lookup unavailable")
	}
	password, err := a.getCardDAVPassword(source.ID)
	if err != nil {
		return nil, source.ID, fmt.Errorf("get password for source %s: %w", source.ID, err)
	}
	client, err := carddav.NewClient(source.URL, source.Username, password)
	if err != nil {
		return nil, source.ID, fmt.Errorf("build carddav client: %w", err)
	}
	return client, source.ID, nil
}

// ListAddressbooks returns the addressbooks for a contact source as the API
// surface type. Backs Contacts_ListAddressbooks which feeds the Add Contact
// dialog's addressbook picker.
//
// Source-type dispatch (Phase 2b.3):
//   - CardDAV: lists the source's enabled addressbooks straight from the local
//     carddav_source_addressbooks table.
//   - Google: surfaces contactGroups as pseudo-addressbooks + a synthetic
//     "My Contacts" entry. Track B fills the live HTTP call.
//   - Microsoft: surfaces contactFolders as addressbooks. Track C fills in.
func (a *API) ListAddressbooks(sourceID string) ([]coreapi.Addressbook, error) {
	if a.carddavStore == nil {
		return nil, nil
	}
	if sourceID == "" {
		return nil, nil
	}
	source, err := a.carddavStore.GetSource(sourceID)
	if err != nil {
		return nil, fmt.Errorf("contacts.ListAddressbooks: lookup source %s: %w", sourceID, err)
	}
	if source == nil {
		return nil, nil
	}
	switch source.Type {
	case carddav.SourceTypeCardDAV:
		return a.listCardDAVAddressbooks(sourceID)
	case carddav.SourceTypeGoogle:
		return a.listGoogleAddressbooks(source)
	case carddav.SourceTypeMicrosoft:
		return a.listMicrosoftAddressbooks(source)
	}
	return nil, nil
}

// listCardDAVAddressbooks is the legacy CardDAV path — lists the source's
// enabled addressbooks from the local mirror table.
func (a *API) listCardDAVAddressbooks(sourceID string) ([]coreapi.Addressbook, error) {
	abs, err := a.carddavStore.ListAddressbooks(sourceID)
	if err != nil {
		return nil, fmt.Errorf("contacts.ListAddressbooks: %w", err)
	}
	out := make([]coreapi.Addressbook, 0, len(abs))
	for _, ab := range abs {
		if ab == nil || !ab.Enabled {
			continue
		}
		out = append(out, coreapi.Addressbook{
			ID:       ab.ID,
			SourceID: ab.SourceID,
			Name:     ab.Name,
			Path:     ab.Path,
		})
	}
	return out, nil
}

// listGoogleAddressbooks lives in google_api.go. listMicrosoftAddressbooks
// lives in microsoft_api.go (Phase 2b.3 Track C).

// SubscribeToContactEvents is scaffolded; Phase 3+ wires through a core
// event bus once one exists.
func (a *API) SubscribeToContactEvents(types []coreapi.ContactEventType) (<-chan coreapi.ContactEvent, coreapi.Unsubscribe, error) {
	return nil, func() {}, coreapi.ErrUnimplemented
}

// listLocal returns local contacts as one-row-per-record (Phase 2b.2.a),
// with multi-field sub-tables hydrated. Fixes the legacy duplicate-row UX
// wart (one row per email).
func (a *API) listLocal(filter coreapi.ContactFilter) ([]coreapi.Contact, error) {
	if a.localStore == nil {
		return nil, nil
	}
	kind := localKindFromSourceID(filter.SourceID)
	records, err := a.localStore.ListRecords(contact.RecordFilter{
		Source: "local",
		Kind:   kind,
		Query:  filter.Query,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("contacts.ListContacts (local): %w", err)
	}
	out := make([]coreapi.Contact, 0, len(records))
	for _, rec := range records {
		out = append(out, fromRecord(rec))
	}
	return out, nil
}

// listCardDAV returns CardDAV contacts as one-row-per-record, scoped to a
// specific source. Uses ListRecordIDsForSource (which JOINs through addressbooks
// to the source) + per-id contact.Store.GetRecord to hydrate the full
// multi-field shape.
func (a *API) listCardDAV(filter coreapi.ContactFilter) ([]coreapi.Contact, error) {
	if a.carddavStore == nil || a.localStore == nil {
		return nil, nil
	}
	ids, err := a.carddavStore.ListRecordIDsForSource(filter.SourceID, filter.Query, filter.Offset, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("contacts.ListContacts (carddav %s): %w", filter.SourceID, err)
	}
	out := make([]coreapi.Contact, 0, len(ids))
	for _, id := range ids {
		rec, err := a.localStore.GetRecord(id)
		if err != nil {
			continue
		}
		if rec == nil {
			continue
		}
		c := fromRecord(rec)
		// Override SourceID with the actual sidebar source UUID the caller
		// asked for (the record's source_ref is the addressbook id, not the
		// source id — the join in ListRecordIDsForSource scoped to the source).
		c.SourceID = filter.SourceID
		out = append(out, c)
	}
	return out, nil
}

func (a *API) listMerged(filter coreapi.ContactFilter) ([]coreapi.Contact, error) {
	if a.localStore == nil {
		return nil, nil
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	// "All" view: always merge local + vCard + CardDAV via contact.Store.Search.
	// Empty query → LIKE '%%' in each source's SQL = match all. The merge +
	// dedupe by email happens inside contact.Store.Search (which uses the
	// carddavSearchFn bridge wired in app.go Startup). Offset is unsupported
	// by Search; callers paginate by raising limit until "more" is needed.
	return a.SearchContacts(filter.Query, limit)
}

// ResizeContactPhoto takes a base64-encoded image (PNG / JPEG / WEBP / GIF),
// rescales it to a max edge of 256px preserving aspect ratio, and re-encodes
// as JPEG at quality 85. Returns the resized base64 + "image/jpeg" as the
// media type ready to drop into a coreapi.ContactPatch.Photo.
//
// The contacts Edit dialog calls this after the frontend HTML file input
// hands over a picked image (matches the picker pattern Composer.svelte
// uses for inline images — pure frontend pick, backend processing).
// Image processing logic lives in extensions/contacts/backend/imaging
// because it's contacts-specific; nothing else in the codebase needs it.
func (a *API) ResizeContactPhoto(b64In string) (b64Out string, mediaType string, err error) {
	b64In = strings.TrimSpace(b64In)
	if b64In == "" {
		return "", "", fmt.Errorf("contacts.ResizeContactPhoto: empty input")
	}
	raw, err := base64.StdEncoding.DecodeString(b64In)
	if err != nil {
		return "", "", fmt.Errorf("contacts.ResizeContactPhoto: decode base64: %w", err)
	}
	jpegBytes, mt, err := imaging.ResizeToJPEG(raw, imaging.ResizeOptions{
		MaxEdge: 256,
		Quality: 85,
	})
	if err != nil {
		return "", "", fmt.Errorf("contacts.ResizeContactPhoto: %w", err)
	}
	return base64.StdEncoding.EncodeToString(jpegBytes), mt, nil
}
