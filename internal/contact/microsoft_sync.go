// Package contact provides contact sync and autocomplete functionality
package contact

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// MicrosoftContactsSyncer syncs contacts from Microsoft Graph API.
// Uses the /me/contacts endpoint to fetch all user's Outlook contacts.
type MicrosoftContactsSyncer struct {
	httpClient *http.Client
	log        zerolog.Logger
}

// NewMicrosoftContactsSyncer creates a new Microsoft contacts syncer.
func NewMicrosoftContactsSyncer() *MicrosoftContactsSyncer {
	return &MicrosoftContactsSyncer{
		httpClient: &http.Client{Timeout: 60 * time.Second}, // Longer timeout for sync
		log:        logging.WithComponent("microsoft-contacts-sync"),
	}
}

// msFolderDefault is the token-map key for the default Contacts folder.
// Graph's /me/contactFolders does NOT list the default folder (documented
// Graph known issue), so full coverage is /me/contacts + each contactFolder.
const msFolderDefault = "default"

// errMSDeltaExpired signals a per-folder deltaLink rejected by Graph
// (410/404). The orchestrator restarts the WHOLE sync as full — the store's
// full-sync path clears-and-replaces the addressbook, so folders must never
// mix full and incremental results in one SyncResult.
var errMSDeltaExpired = fmt.Errorf("microsoft delta link expired")

// SyncContactsDelta syncs the default Contacts folder plus every user
// contactFolder (#278 — the previous /me/contacts-only sync missed all
// non-default folders). syncToken is a JSON map of folder key -> deltaLink;
// empty, legacy plain-string, or incomplete (a folder appeared) tokens
// trigger a full sync of everything.
func (s *MicrosoftContactsSyncer) SyncContactsDelta(accessToken, syncToken string) (*SyncResult, error) {
	folderIDs, err := s.listContactFolderIDs(accessToken)
	if err != nil {
		return nil, err
	}
	keys := append([]string{msFolderDefault}, folderIDs...)

	tokens := parseMSFolderTokens(syncToken)
	isFullSync := false
	for _, k := range keys {
		if tokens[k] == "" {
			isFullSync = true
			break
		}
	}
	if isFullSync {
		tokens = map[string]string{}
		s.log.Info().Int("folders", len(keys)).Msg("Starting Microsoft contacts full sync")
	}
	if !isFullSync {
		s.log.Info().Int("folders", len(keys)).Msg("Starting Microsoft contacts incremental sync")
	}

	var allRecords []SyncedRecord
	var deletedIDs []string
	newTokens := make(map[string]string, len(keys))
	for _, k := range keys {
		recs, dels, dl, folderErr := s.syncFolderDelta(accessToken, k, tokens[k])
		if folderErr == errMSDeltaExpired {
			s.log.Warn().Str("folder", k).Msg("Microsoft delta link expired, restarting as full sync")
			return s.SyncContactsDelta(accessToken, "")
		}
		if folderErr != nil {
			return nil, folderErr
		}
		allRecords = append(allRecords, recs...)
		deletedIDs = append(deletedIDs, dels...)
		if dl != "" {
			newTokens[k] = dl
		}
	}

	// Download photo bytes per contact (Graph doesn't return them inline).
	s.enrichPhotos(accessToken, allRecords)

	tokenJSON, _ := json.Marshal(newTokens)
	syncResult := &SyncResult{
		Records:       allRecords,
		DeletedIDs:    deletedIDs,
		NextSyncToken: string(tokenJSON),
		IsFullSync:    isFullSync,
	}

	if isFullSync {
		s.log.Info().
			Int("total_records", len(allRecords)).
			Int("folders", len(keys)).
			Msg("Microsoft contacts full sync completed")
		return syncResult, nil
	}
	s.log.Info().
		Int("updated_records", len(allRecords)).
		Int("deleted_contacts", len(deletedIDs)).
		Msg("Microsoft contacts incremental sync completed")
	return syncResult, nil
}

// parseMSFolderTokens decodes the per-folder token map. Legacy plain
// deltaLink strings (pre-folder-sync) and malformed values yield an empty
// map, forcing one clean full resync.
func parseMSFolderTokens(syncToken string) map[string]string {
	tokens := map[string]string{}
	if syncToken == "" {
		return tokens
	}
	if err := json.Unmarshal([]byte(syncToken), &tokens); err != nil {
		return map[string]string{}
	}
	return tokens
}

// listContactFolderIDs enumerates the user's non-default contact folders
// (paginated). The default folder is intentionally absent from this endpoint.
func (s *MicrosoftContactsSyncer) listContactFolderIDs(accessToken string) ([]string, error) {
	var ids []string
	nextLink := "https://graph.microsoft.com/v1.0/me/contactFolders?$select=id&$top=100"
	for nextLink != "" {
		req, err := http.NewRequest("GET", nextLink, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Microsoft Graph API request failed: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("Microsoft Graph contactFolders error %d: %s", resp.StatusCode, string(bodyBytes))
		}
		var result struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to parse contactFolders response: %w", err)
		}
		resp.Body.Close()
		for _, f := range result.Value {
			ids = append(ids, f.ID)
		}
		nextLink = result.NextLink
	}
	return ids, nil
}

// syncFolderDelta runs the Graph delta loop for one folder (key
// msFolderDefault = /me/contacts, otherwise /me/contactFolders/{id}).
// Returns the folder's records, deleted ids, and its next deltaLink;
// errMSDeltaExpired when an incremental deltaLink is rejected.
func (s *MicrosoftContactsSyncer) syncFolderDelta(accessToken, folderKey, deltaLink string) ([]SyncedRecord, []string, string, error) {
	var allRecords []SyncedRecord
	var deletedIDs []string
	isFullSync := deltaLink == ""

	// Determine starting URL
	// Note: The delta endpoint doesn't support $select, $top, $orderby, $filter, $expand, $search
	nextLink := deltaLink
	switch {
	case nextLink != "":
		// Incremental: continue from the stored deltaLink
	case folderKey == msFolderDefault:
		nextLink = "https://graph.microsoft.com/v1.0/me/contacts/delta"
	default:
		nextLink = "https://graph.microsoft.com/v1.0/me/contactFolders/" + url.PathEscape(folderKey) + "/contacts/delta"
	}

	var finalDeltaLink string

	for nextLink != "" {
		req, err := http.NewRequest("GET", nextLink, nil)
		if err != nil {
			return nil, nil, "", fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			s.log.Error().Err(err).Msg("Microsoft Graph API request failed")
			return nil, nil, "", fmt.Errorf("Microsoft Graph API request failed: %w", err)
		}

		// Handle 410 Gone or 404 - delta token expired; the orchestrator
		// restarts the whole sync as full
		if resp.StatusCode == http.StatusGone || (resp.StatusCode == http.StatusNotFound && !isFullSync) {
			resp.Body.Close()
			return nil, nil, "", errMSDeltaExpired
		}

		if resp.StatusCode != http.StatusOK {
			// Read the error response body for error handling
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			s.log.Error().
				Int("status", resp.StatusCode).
				Msg("Microsoft Graph API error response")

			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return nil, nil, "", fmt.Errorf("Microsoft API authentication failed: %s", string(bodyBytes))
			case http.StatusForbidden:
				return nil, nil, "", fmt.Errorf("Microsoft API access denied: %s", string(bodyBytes))
			case http.StatusTooManyRequests:
				return nil, nil, "", fmt.Errorf("Microsoft API rate limit exceeded")
			default:
				return nil, nil, "", fmt.Errorf("Microsoft Graph API error %d: %s", resp.StatusCode, string(bodyBytes))
			}
		}

		// Parse response
		var result msGraphDeltaResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, nil, "", fmt.Errorf("failed to parse Microsoft API response: %w", err)
		}
		resp.Body.Close()

		// Convert to full records (email optional — a phone-only contact is
		// still a valid record).
		for _, c := range result.Value {
			// Check if this is a deleted contact
			if c.Removed != nil {
				deletedIDs = append(deletedIDs, c.ID)
				continue
			}
			rec := msContactToRecord(c)
			if rec == nil {
				continue
			}
			allRecords = append(allRecords, SyncedRecord{
				Record:   rec,
				RemoteID: c.ID,
				ETag:     c.ETag,
			})
		}

		s.log.Debug().
			Str("folder", folderKey).
			Int("page_count", len(result.Value)).
			Int("records_so_far", len(allRecords)).
			Int("deleted_so_far", len(deletedIDs)).
			Msg("Fetched Microsoft contacts page")

		// Check for more pages or final delta link
		nextLink = result.NextLink
		if nextLink == "" {
			finalDeltaLink = result.DeltaLink
		}
	}

	return allRecords, deletedIDs, finalDeltaLink, nil
}

// enrichPhotos downloads each contact's photo from Graph and stores it inline.
// Graph never returns contact photos in the delta payload — each one is a
// separate GET /me/contacts/{id}/photo/$value (binary). Best-effort and
// sequential: 404 (the common "no photo" case) and any other failure simply
// leave the record photo-less. On the first full sync this probes every
// contact; incremental delta syncs only revisit changed contacts.
func (s *MicrosoftContactsSyncer) enrichPhotos(accessToken string, records []SyncedRecord) {
	for _, sr := range records {
		if sr.Record == nil || sr.RemoteID == "" {
			continue
		}
		photoURL := "https://graph.microsoft.com/v1.0/me/contacts/" + url.PathEscape(sr.RemoteID) + "/photo/$value"
		req, err := http.NewRequest("GET", photoURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		data, mediaType, ok := fetchInlinePhoto(s.httpClient, req, maxInlinePhotoBytes)
		if !ok {
			continue
		}
		sr.Record.PhotoData = data
		sr.Record.PhotoMediaType = mediaType
	}
}

// msContactToRecord maps a Graph contact into the shared multi-field Record.
// Email is optional — a phone-only contact still yields a record. Returns nil
// only when the contact carries no usable data at all (no name, email, or
// phone) so it would be an empty row.
func msContactToRecord(c msGraphDeltaContact) *Record {
	rec := &Record{
		Source:  "carddav",
		Fn:      c.DisplayName,
		NGiven:  c.GivenName,
		NFamily: c.Surname,
		Org:     c.CompanyName,
		Title:   c.JobTitle,
	}
	for _, e := range c.EmailAddresses {
		if e.Address == "" {
			continue
		}
		rec.Emails = append(rec.Emails, RecordEmail{Email: e.Address})
	}
	for _, n := range c.HomePhones {
		if n == "" {
			continue
		}
		rec.Phones = append(rec.Phones, RecordPhone{Number: n, PhoneType: "home"})
	}
	for _, n := range c.BusinessPhones {
		if n == "" {
			continue
		}
		rec.Phones = append(rec.Phones, RecordPhone{Number: n, PhoneType: "work"})
	}
	if c.MobilePhone != "" {
		rec.Phones = append(rec.Phones, RecordPhone{Number: c.MobilePhone, PhoneType: "cell"})
	}
	rec.Addresses = appendMSAddress(rec.Addresses, "home", c.HomeAddress)
	rec.Addresses = appendMSAddress(rec.Addresses, "work", c.BusinessAddress)
	rec.Addresses = appendMSAddress(rec.Addresses, "other", c.OtherAddress)

	if rec.Fn == "" && len(rec.Emails) == 0 && len(rec.Phones) == 0 {
		return nil
	}
	return rec
}

// appendMSAddress appends a structured address only when it carries any field.
func appendMSAddress(addrs []RecordAddress, typ string, a msGraphAddress) []RecordAddress {
	if a.Street == "" && a.City == "" && a.State == "" && a.PostalCode == "" && a.CountryOrRegion == "" {
		return addrs
	}
	return append(addrs, RecordAddress{
		AddrType: typ,
		Street:   a.Street,
		City:     a.City,
		Region:   a.State,
		Postcode: a.PostalCode,
		Country:  a.CountryOrRegion,
	})
}

// Microsoft Graph API contacts response structures

// msGraphDeltaResponse is used for delta sync responses
type msGraphDeltaResponse struct {
	Value     []msGraphDeltaContact `json:"value"`
	NextLink  string                `json:"@odata.nextLink"`
	DeltaLink string                `json:"@odata.deltaLink"` // Final link for next incremental sync
}

// msGraphDeltaContact represents a contact from a delta sync, with optional
// removal info. Fields mirror the default `/me/contacts/delta` projection
// (delta forbids $select, so the server returns this full set).
type msGraphDeltaContact struct {
	ID              string              `json:"id"`
	ETag            string              `json:"@odata.etag"`
	DisplayName     string              `json:"displayName"`
	GivenName       string              `json:"givenName"`
	Surname         string              `json:"surname"`
	CompanyName     string              `json:"companyName"`
	JobTitle        string              `json:"jobTitle"`
	EmailAddresses  []msGraphEmail      `json:"emailAddresses"`
	HomePhones      []string            `json:"homePhones"`
	BusinessPhones  []string            `json:"businessPhones"`
	MobilePhone     string              `json:"mobilePhone"`
	HomeAddress     msGraphAddress      `json:"homeAddress"`
	BusinessAddress msGraphAddress      `json:"businessAddress"`
	OtherAddress    msGraphAddress      `json:"otherAddress"`
	Removed         *msGraphRemovedInfo `json:"@removed,omitempty"` // Present when contact was deleted
}

type msGraphRemovedInfo struct {
	Reason string `json:"reason"` // "changed" or "deleted"
}

type msGraphEmail struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

type msGraphAddress struct {
	Street          string `json:"street"`
	City            string `json:"city"`
	State           string `json:"state"`
	PostalCode      string `json:"postalCode"`
	CountryOrRegion string `json:"countryOrRegion"`
}
