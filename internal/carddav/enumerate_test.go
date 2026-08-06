package carddav

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mailfenceServer replicates the minimal DAV behavior from issue #366:
// sync-collection REPORTs get 501, addressbook-query REPORTs get 409, and
// only PROPFIND enumeration + (configurably) multiget or plain GET work.
// Round-2 quirks baked in: multiget responses volunteer a malformed
// getlastmodified (non-RFC1123 day), and GETs serve the legacy text/x-vcard
// MIME type (overridable via getContentType).
type mailfenceServer struct {
	srv            *httptest.Server
	rejectMultiget bool
	getContentType string
	requests       []string
}

const (
	vcardAlice = "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:a1\r\nFN:Alice Test\r\nEMAIL:alice@example.com\r\nEND:VCARD\r\n"
	vcardBob   = "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:b1\r\nFN:Bob Test\r\nEMAIL:bob@example.com\r\nEND:VCARD\r\n"
)

func newMailfenceServer(rejectMultiget bool) *mailfenceServer {
	m := &mailfenceServer{rejectMultiget: rejectMultiget, getContentType: "text/x-vcard"}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	return m
}

func (m *mailfenceServer) close() { m.srv.Close() }

func (m *mailfenceServer) handle(w http.ResponseWriter, r *http.Request) {
	body := make([]byte, 0, 1024)
	if r.Body != nil {
		buf := make([]byte, 64<<10)
		n, _ := r.Body.Read(buf)
		body = buf[:n]
		r.Body.Close()
	}
	m.requests = append(m.requests, r.Method+" "+r.URL.Path)

	switch {
	case r.Method == "REPORT" && strings.Contains(string(body), "sync-collection"):
		w.WriteHeader(http.StatusNotImplemented)
	case r.Method == "REPORT" && strings.Contains(string(body), "addressbook-query"):
		w.WriteHeader(http.StatusConflict)
	case r.Method == "REPORT" && strings.Contains(string(body), "addressbook-multiget"):
		if m.rejectMultiget {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		// Mailfence volunteers getlastmodified with a non-RFC1123 day even
		// when not requested (#366) — the parser must ignore it.
		w.Write([]byte(`<?xml version="1.0"?>` + //nolint:errcheck
			`<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">` +
			`<d:response><d:href>/addressbook/alice.vcf</d:href><d:propstat><d:prop>` +
			`<d:getlastmodified>Tue, 8 Jul 2025 19:41:18 GMT</d:getlastmodified>` +
			`<d:getetag>"e-alice"</d:getetag><card:address-data>` + vcardAlice + `</card:address-data>` +
			`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
			`<d:response><d:href>/addressbook/bob.vcf</d:href><d:propstat><d:prop>` +
			`<d:getlastmodified>Tue, 8 Jul 2025 19:41:18 GMT</d:getlastmodified>` +
			`<d:getetag>"e-bob"</d:getetag><card:address-data>` + vcardBob + `</card:address-data>` +
			`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
			`</d:multistatus>`))
	case r.Method == "PROPFIND" && strings.HasPrefix(r.URL.Path, "/addressbook"):
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		w.Write([]byte(`<?xml version="1.0"?>` + //nolint:errcheck
			`<d:multistatus xmlns:d="DAV:">` +
			`<d:response><d:href>/addressbook/</d:href><d:propstat><d:prop>` +
			`<d:resourcetype><d:collection/></d:resourcetype>` +
			`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
			`<d:response><d:href>/addressbook/alice.vcf</d:href><d:propstat><d:prop>` +
			`<d:resourcetype/><d:getetag>"e-alice"</d:getetag><d:getcontenttype>text/vcard</d:getcontenttype>` +
			`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
			`<d:response><d:href>/addressbook/bob.vcf</d:href><d:propstat><d:prop>` +
			`<d:resourcetype/><d:getetag>"e-bob"</d:getetag>` +
			`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>` +
			`</d:multistatus>`))
	case r.Method == "GET" && r.URL.Path == "/addressbook/alice.vcf":
		w.Header().Set("Content-Type", m.getContentType+"; charset=utf-8")
		w.Header().Set("ETag", `"e-alice"`)
		w.Write([]byte(vcardAlice)) //nolint:errcheck
	case r.Method == "GET" && r.URL.Path == "/addressbook/bob.vcf":
		w.Header().Set("Content-Type", m.getContentType+"; charset=utf-8")
		w.Header().Set("ETag", `"e-bob"`)
		w.Write([]byte(vcardBob)) //nolint:errcheck
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func assertAliceBob(t *testing.T, records []*ParsedRecord) {
	t.Helper()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	names := map[string]bool{}
	for _, r := range records {
		names[r.FN] = true
	}
	if !names["Alice Test"] || !names["Bob Test"] {
		t.Fatalf("parsed FNs = %v, want Alice Test + Bob Test", names)
	}
}

func TestFetchContactsEnumerate_Multiget(t *testing.T) {
	m := newMailfenceServer(false)
	defer m.close()

	c := newTestClient(t, m.srv.URL)
	records, err := c.FetchContactsEnumerate("/addressbook/")
	if err != nil {
		t.Fatalf("FetchContactsEnumerate: %v", err)
	}
	assertAliceBob(t, records)
}

// TestFetchContactsEnumerate_GetRejectsUnknownContentType proves the GET
// fallback's MIME whitelist stays strict: text/vcard and legacy text/x-vcard
// pass, anything else errors.
func TestFetchContactsEnumerate_GetRejectsUnknownContentType(t *testing.T) {
	m := newMailfenceServer(true)
	defer m.close()
	m.getContentType = "text/html"

	c := newTestClient(t, m.srv.URL)
	_, err := c.FetchContactsEnumerate("/addressbook/")
	if err == nil {
		t.Fatal("expected error for Content-Type text/html, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected Content-Type") {
		t.Fatalf("expected Content-Type rejection, got: %v", err)
	}
}

func TestFetchContactsEnumerate_GetFallback(t *testing.T) {
	m := newMailfenceServer(true)
	defer m.close()

	c := newTestClient(t, m.srv.URL)
	records, err := c.FetchContactsEnumerate("/addressbook/")
	if err != nil {
		t.Fatalf("FetchContactsEnumerate (GET fallback): %v", err)
	}
	assertAliceBob(t, records)

	var gets int
	for _, req := range m.requests {
		if strings.HasPrefix(req, "GET ") {
			gets++
		}
	}
	if gets != 2 {
		t.Fatalf("server saw %d GETs, want 2 (per-href fallback); requests=%v", gets, m.requests)
	}
}

// TestSyncAddressbookLegacy_MailfenceChain drives the full tier chain the way
// issue #366 hits it: sync-collection 501 → addressbook-query 409 → PROPFIND
// enumeration succeeds and the records land in the store with href+etag state.
func TestSyncAddressbookLegacy_MailfenceChain(t *testing.T) {
	m := newMailfenceServer(false)
	defer m.close()

	db := openCardDAVTestDB(t)
	store := NewStore(db.DB)
	syncer := NewSyncer(store, nil)

	source, err := store.CreateSource(&SourceConfig{Name: "mf", Type: SourceTypeCardDAV, URL: m.srv.URL, Username: "user", Enabled: true})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	ab, err := store.CreateAddressbook(source.ID, "/addressbook/", "Mike Test", true)
	if err != nil {
		t.Fatalf("CreateAddressbook: %v", err)
	}

	client := newTestClient(t, m.srv.URL)
	if err := syncer.syncAddressbookFull(client, ab); err != nil {
		t.Fatalf("syncAddressbookFull: %v", err)
	}

	var stateRows int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM carddav_record_state WHERE addressbook_id = ? AND etag != ''`, ab.ID).Scan(&stateRows); err != nil {
		t.Fatalf("count record state: %v", err)
	}
	if stateRows != 2 {
		t.Fatalf("carddav_record_state rows = %d, want 2", stateRows)
	}
	var recordRows int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM contact_records WHERE source = 'carddav'`).Scan(&recordRows); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if recordRows != 2 {
		t.Fatalf("contact_records rows = %d, want 2", recordRows)
	}
}
