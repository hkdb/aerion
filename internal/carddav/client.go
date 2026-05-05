package carddav

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/carddav"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// xmlFixTransport wraps an http.RoundTripper to normalize WebDAV XML responses:
//  1. DAV:getlastmodified — converts numeric timezone offsets (e.g., +0000) to GMT format.
//     Some servers (e.g., Purelymail) return RFC 1123Z dates which http.ParseTime() cannot parse.
//  2. DAV:getetag — adds quotes around unquoted ETag values.
//     Some servers (e.g., mailbox.org) return unquoted ETags which go-webdav's strconv.Unquote() rejects.
type xmlFixTransport struct {
	base http.RoundTripper
}

var getlastmodifiedRe = regexp.MustCompile(
	`(<[^>]*getlastmodified[^>]*>)\s*([^<]+?)\s*(</[^>]*getlastmodified[^>]*>)`,
)

var getetagRe = regexp.MustCompile(
	`(<[^>]*getetag[^>]*>)\s*([^<]+?)\s*(</[^>]*getetag[^>]*>)`,
)

func (t *xmlFixTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "xml") && !strings.Contains(ct, "text/xml") {
		return resp, nil
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("xmlFixTransport: failed to read body: %w", err)
	}

	// Fix 1: Normalize getlastmodified date formats
	fixed := getlastmodifiedRe.ReplaceAllFunc(body, func(match []byte) []byte {
		sub := getlastmodifiedRe.FindSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		dateStr := strings.TrimSpace(string(sub[2]))
		return fixDateValue(sub[1], dateStr, sub[3])
	})

	// Fix 2: Quote unquoted getetag values
	fixed = getetagRe.ReplaceAllFunc(fixed, func(match []byte) []byte {
		sub := getetagRe.FindSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		etagStr := strings.TrimSpace(string(sub[2]))
		return fixETagValue(sub[1], etagStr, sub[3])
	})

	resp.Body = io.NopCloser(bytes.NewReader(fixed))
	resp.ContentLength = int64(len(fixed))
	return resp, nil
}

// fixETagValue normalizes an ETag value for go-webdav's strconv.Unquote().
// Handles: literal quotes, XML-entity-encoded quotes (&quot;), weak ETags (W/), unquoted values.
// Operates on raw XML bytes (before XML entity resolution).
func fixETagValue(prefix []byte, etagStr string, suffix []byte) []byte {
	var buf bytes.Buffer
	buf.Write(prefix)

	cleaned := etagStr

	// Strip weak ETag prefix if present
	if strings.HasPrefix(cleaned, "W/") || strings.HasPrefix(cleaned, "w/") {
		cleaned = cleaned[2:]
	}

	// Already quoted with literal quotes — leave as-is
	if strings.HasPrefix(cleaned, `"`) && strings.HasSuffix(cleaned, `"`) && len(cleaned) >= 2 {
		buf.WriteString(cleaned)
		buf.Write(suffix)
		return buf.Bytes()
	}

	// Quoted with XML-entity-encoded quotes (&quot;...&quot;) — leave as-is
	// The XML parser will resolve these to literal quotes before go-webdav sees them.
	if strings.HasPrefix(cleaned, "&quot;") && strings.HasSuffix(cleaned, "&quot;") {
		buf.WriteString(cleaned)
		buf.Write(suffix)
		return buf.Bytes()
	}

	// Truly unquoted — wrap in literal quotes
	cleaned = strings.Trim(cleaned, `"`)
	buf.WriteByte('"')
	buf.WriteString(cleaned)
	buf.WriteByte('"')
	buf.Write(suffix)
	return buf.Bytes()
}

// fixDateValue converts an RFC 1123Z date to RFC 1123 (GMT) format.
// If the value is not RFC 1123Z, it is returned unchanged.
func fixDateValue(prefix []byte, dateStr string, suffix []byte) []byte {
	t, err := time.Parse(time.RFC1123Z, dateStr)
	if err != nil {
		// Not RFC 1123Z — leave unchanged
		var buf bytes.Buffer
		buf.Write(prefix)
		buf.WriteString(dateStr)
		buf.Write(suffix)
		return buf.Bytes()
	}
	var buf bytes.Buffer
	buf.Write(prefix)
	buf.WriteString(t.UTC().Format(http.TimeFormat))
	buf.Write(suffix)
	return buf.Bytes()
}

// newHTTPClient creates an HTTP client with the XML-fix transport applied.
func newHTTPClient(timeout time.Duration) *http.Client {
	base := http.DefaultTransport
	return &http.Client{
		Timeout:   timeout,
		Transport: &xmlFixTransport{base: base},
	}
}

// Client wraps the CardDAV client with discovery and convenience methods
type Client struct {
	client   *carddav.Client
	baseURL  string
	username string
	password string
	log      zerolog.Logger
}

// NewClient creates a new CardDAV client
func NewClient(baseURL, username, password string) (*Client, error) {
	// Create HTTP client with XML-fix transport
	httpClient := newHTTPClient(30 * time.Second)

	// Parse and normalize the URL
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Ensure scheme is present
	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "https"
	}

	client, err := carddav.NewClient(
		webdav.HTTPClientWithBasicAuth(httpClient, username, password),
		parsedURL.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CardDAV client: %w", err)
	}

	return &Client{
		client:   client,
		baseURL:  parsedURL.String(),
		username: username,
		password: password,
		log:      logging.WithComponent("carddav-client"),
	}, nil
}

// DiscoverAddressbooks discovers all addressbooks from the server
// It tries multiple discovery methods:
// 1. .well-known/carddav
// 2. Direct PROPFIND on the URL
// 3. Common paths (/remote.php/dav for Nextcloud, etc.)
func DiscoverAddressbooks(baseURL, username, password string) ([]AddressbookInfo, error) {
	ctx := context.Background()
	log := logging.WithComponent("carddav-discovery")
	log.Info().Str("url", baseURL).Msg("Starting addressbook discovery")

	// Parse URL
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "https"
	}

	// Create HTTP client with XML-fix transport
	httpClient := webdav.HTTPClientWithBasicAuth(
		newHTTPClient(30*time.Second),
		username, password,
	)

	// Try discovery methods in order
	var addressbooks []AddressbookInfo

	// Method 1: Try the URL as-is (might be a direct addressbook URL or principal)
	addressbooks, err = tryDiscoverFromURL(ctx, httpClient, parsedURL.String(), log)
	if err == nil && len(addressbooks) > 0 {
		return addressbooks, nil
	}
	log.Debug().Err(err).Msg("Direct URL discovery failed, trying .well-known")

	// Method 2: Try .well-known/carddav
	wellKnownURL := fmt.Sprintf("%s://%s/.well-known/carddav", parsedURL.Scheme, parsedURL.Host)
	addressbooks, err = tryDiscoverFromURL(ctx, httpClient, wellKnownURL, log)
	if err == nil && len(addressbooks) > 0 {
		return addressbooks, nil
	}
	log.Debug().Err(err).Msg(".well-known discovery failed, trying common paths")

	// Method 3: Try common CardDAV paths
	commonPaths := []string{
		"/remote.php/dav",     // Nextcloud/ownCloud
		"/remote.php/carddav", // Older Nextcloud
		fmt.Sprintf("/remote.php/dav/addressbooks/users/%s/", username), // Nextcloud direct
		"/dav",                    // Generic
		"/carddav",                // Generic
		"/principals/" + username, // Some servers
	}

	for _, path := range commonPaths {
		tryURL := fmt.Sprintf("%s://%s%s", parsedURL.Scheme, parsedURL.Host, path)
		addressbooks, err = tryDiscoverFromURL(ctx, httpClient, tryURL, log)
		if err == nil && len(addressbooks) > 0 {
			return addressbooks, nil
		}
	}

	return nil, fmt.Errorf("no addressbooks found at %s", baseURL)
}

// tryDiscoverFromURL attempts to discover addressbooks from a specific URL
func tryDiscoverFromURL(ctx context.Context, httpClient webdav.HTTPClient, urlStr string, log zerolog.Logger) ([]AddressbookInfo, error) {
	log.Debug().Str("url", urlStr).Msg("Trying discovery from URL")

	client, err := carddav.NewClient(httpClient, urlStr)
	if err != nil {
		return nil, err
	}

	// Try to find the current user's principal
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		log.Debug().Err(err).Msg("FindCurrentUserPrincipal failed")
		// Try the URL directly as addressbook home
		return tryListAddressbooksAt(ctx, httpClient, urlStr, log)
	}

	log.Debug().Str("principal", principal).Msg("Found principal")

	// Find addressbook home set
	homeSet, err := client.FindAddressBookHomeSet(ctx, principal)
	if err != nil {
		log.Debug().Err(err).Msg("FindAddressBookHomeSet failed")
		return nil, err
	}

	log.Debug().Str("homeSet", homeSet).Msg("Found addressbook home set")

	// List addressbooks in the home set
	return tryListAddressbooksAt(ctx, httpClient, resolveURL(urlStr, homeSet), log)
}

// tryListAddressbooksAt lists addressbooks at a specific URL
func tryListAddressbooksAt(ctx context.Context, httpClient webdav.HTTPClient, urlStr string, log zerolog.Logger) ([]AddressbookInfo, error) {
	client, err := carddav.NewClient(httpClient, urlStr)
	if err != nil {
		return nil, err
	}

	// Extract path from URL - FindAddressBooks expects a path, not a full URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	log.Debug().Str("url", urlStr).Str("path", parsedURL.Path).Msg("Listing addressbooks")

	addressbooks, err := client.FindAddressBooks(ctx, parsedURL.Path)
	if err != nil {
		return nil, err
	}

	var result []AddressbookInfo
	for _, ab := range addressbooks {
		info := AddressbookInfo{
			Path:        ab.Path,
			Name:        ab.Name,
			Description: ab.Description,
		}
		if info.Name == "" {
			// Use the last path segment as the name
			parts := strings.Split(strings.Trim(ab.Path, "/"), "/")
			if len(parts) > 0 {
				info.Name = parts[len(parts)-1]
			}
		}
		result = append(result, info)
		log.Debug().Str("path", ab.Path).Str("name", ab.Name).Msg("Found addressbook")
	}

	return result, nil
}

// resolveURL resolves a potentially relative URL against a base URL
func resolveURL(baseURL, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return path
	}

	ref, err := url.Parse(path)
	if err != nil {
		return path
	}

	return base.ResolveReference(ref).String()
}

// FetchContacts fetches all contacts from an addressbook
func (c *Client) FetchContacts(addressbookPath string) ([]ParsedContact, error) {
	ctx := context.Background()
	c.log.Debug().Str("path", addressbookPath).Msg("Fetching contacts")

	// Resolve the addressbook path against the base URL
	fullPath := resolveURL(c.baseURL, addressbookPath)

	// Create a new client for this specific addressbook
	httpClient := webdav.HTTPClientWithBasicAuth(
		newHTTPClient(60*time.Second),
		c.username, c.password,
	)

	abClient, err := carddav.NewClient(httpClient, fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for addressbook: %w", err)
	}

	// Query all address objects
	query := &carddav.AddressBookQuery{
		DataRequest: carddav.AddressDataRequest{
			AllProp: true,
		},
	}

	addressObjects, err := abClient.QueryAddressBook(ctx, addressbookPath, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query addressbook: %w", err)
	}

	c.log.Debug().Int("count", len(addressObjects)).Msg("Fetched address objects")

	var contacts []ParsedContact
	for _, obj := range addressObjects {
		parsed := parseVCard(obj)
		contacts = append(contacts, parsed...)
	}

	c.log.Info().Int("contacts", len(contacts)).Str("path", addressbookPath).Msg("Parsed contacts from addressbook")
	return contacts, nil
}

// ParsedContact represents a contact parsed from vCard data
type ParsedContact struct {
	Href        string
	ETag        string
	Email       string
	DisplayName string
}

// parseVCard parses a vCard and extracts contacts (one per email address)
func parseVCard(obj carddav.AddressObject) []ParsedContact {
	if obj.Card == nil {
		return nil
	}

	card := obj.Card

	// Get display name
	displayName := ""
	if fn := card.PreferredValue(vcard.FieldFormattedName); fn != "" {
		displayName = fn
	} else if n := card.Name(); n != nil {
		parts := []string{}
		if n.GivenName != "" {
			parts = append(parts, n.GivenName)
		}
		if n.FamilyName != "" {
			parts = append(parts, n.FamilyName)
		}
		displayName = strings.Join(parts, " ")
	}

	// Get all email addresses
	emails := card.Values(vcard.FieldEmail)
	if len(emails) == 0 {
		return nil
	}

	var contacts []ParsedContact
	for _, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}

		contacts = append(contacts, ParsedContact{
			Href:        obj.Path,
			ETag:        obj.ETag,
			Email:       email,
			DisplayName: displayName,
		})
	}

	return contacts
}

// SyncResult represents the result of an incremental sync
type SyncResult struct {
	SyncToken string          // New sync token to store
	Updated   []ParsedContact // Contacts that were added/modified
	Deleted   []string        // Hrefs of contacts that were deleted
}

// SyncAddressbook performs an incremental sync using sync-collection
// If syncToken is empty, it performs a full sync
// Returns the new sync token and the changes since the last sync
func (c *Client) SyncAddressbook(addressbookPath, syncToken string) (*SyncResult, error) {
	ctx := context.Background()
	c.log.Debug().
		Str("path", addressbookPath).
		Str("syncToken", syncToken).
		Msg("Starting sync-collection")

	// Resolve the addressbook path against the base URL
	fullPath := resolveURL(c.baseURL, addressbookPath)

	// Create a new client for this specific addressbook
	httpClient := webdav.HTTPClientWithBasicAuth(
		newHTTPClient(60*time.Second),
		c.username, c.password,
	)

	abClient, err := carddav.NewClient(httpClient, fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for addressbook: %w", err)
	}

	// Perform sync-collection request
	query := &carddav.SyncQuery{
		DataRequest: carddav.AddressDataRequest{
			AllProp: true, // Request full vCard data
		},
		SyncToken: syncToken,
	}

	syncResp, err := abClient.SyncCollection(ctx, addressbookPath, query)
	if err != nil {
		// If sync-collection fails (e.g., invalid token), return error
		// Caller should fall back to full sync
		return nil, fmt.Errorf("sync-collection failed: %w", err)
	}

	c.log.Debug().
		Int("updated", len(syncResp.Updated)).
		Int("deleted", len(syncResp.Deleted)).
		Str("newToken", syncResp.SyncToken).
		Msg("Sync-collection completed")

	result := &SyncResult{
		SyncToken: syncResp.SyncToken,
		Deleted:   syncResp.Deleted,
	}

	// If we have updated items, we need to fetch their full vCard data
	// The sync-collection response may not include full card data
	if len(syncResp.Updated) > 0 {
		// Check if the response includes card data
		hasCardData := false
		for _, obj := range syncResp.Updated {
			if obj.Card != nil && len(obj.Card) > 0 {
				hasCardData = true
				break
			}
		}

		if hasCardData {
			// Parse contacts directly from sync response
			for _, obj := range syncResp.Updated {
				parsed := parseVCard(obj)
				result.Updated = append(result.Updated, parsed...)
			}
		} else {
			// Need to fetch full card data using multiget
			paths := make([]string, len(syncResp.Updated))
			for i, obj := range syncResp.Updated {
				paths[i] = obj.Path
			}

			contacts, err := c.fetchContactsByPath(abClient, addressbookPath, paths)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch updated contacts: %w", err)
			}
			result.Updated = contacts
		}
	}

	c.log.Info().
		Int("updated", len(result.Updated)).
		Int("deleted", len(result.Deleted)).
		Str("path", addressbookPath).
		Msg("Incremental sync completed")

	return result, nil
}

// fetchContactsByPath fetches contacts by their paths using addressbook-multiget
func (c *Client) fetchContactsByPath(client *carddav.Client, addressbookPath string, paths []string) ([]ParsedContact, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	ctx := context.Background()
	c.log.Debug().
		Int("count", len(paths)).
		Msg("Fetching contacts by path using multiget")

	multiGet := &carddav.AddressBookMultiGet{
		Paths: paths,
		DataRequest: carddav.AddressDataRequest{
			AllProp: true,
		},
	}

	addressObjects, err := client.MultiGetAddressBook(ctx, addressbookPath, multiGet)
	if err != nil {
		return nil, fmt.Errorf("multiget failed: %w", err)
	}

	var contacts []ParsedContact
	for _, obj := range addressObjects {
		parsed := parseVCard(obj)
		contacts = append(contacts, parsed...)
	}

	return contacts, nil
}

// TestConnection tests the connection to the CardDAV server
func TestConnection(baseURL, username, password string) error {
	log := logging.WithComponent("carddav-test")
	log.Info().Str("url", baseURL).Msg("Testing CardDAV connection")

	// Try to discover addressbooks - this validates credentials and connectivity
	addressbooks, err := DiscoverAddressbooks(baseURL, username, password)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}

	if len(addressbooks) == 0 {
		return fmt.Errorf("connection successful but no addressbooks found")
	}

	log.Info().Int("addressbooks", len(addressbooks)).Msg("Connection test successful")
	return nil
}
