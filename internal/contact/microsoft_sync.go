// Package contact provides contact sync and autocomplete functionality
package contact

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// SyncContactsDelta performs an incremental sync using Microsoft Graph delta queries.
// If deltaLink is empty, performs a full sync and returns a deltaLink for future incremental syncs.
// If the deltaLink is expired, automatically falls back to full sync.
func (s *MicrosoftContactsSyncer) SyncContactsDelta(accessToken, deltaLink string) (*SyncResult, error) {
	var allRecords []SyncedRecord
	var deletedIDs []string
	isFullSync := deltaLink == ""

	// Determine starting URL
	// Note: The delta endpoint doesn't support $select, $top, $orderby, $filter, $expand, $search
	var nextLink string
	if isFullSync {
		// Full sync: use delta endpoint without token
		nextLink = "https://graph.microsoft.com/v1.0/me/contacts/delta"
		s.log.Info().Msg("Starting Microsoft contacts full sync")
	} else {
		// Incremental sync: use stored deltaLink
		nextLink = deltaLink
		s.log.Info().Msg("Starting Microsoft contacts incremental sync")
	}

	var finalDeltaLink string

	for nextLink != "" {
		req, err := http.NewRequest("GET", nextLink, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			s.log.Error().Err(err).Msg("Microsoft Graph API request failed")
			return nil, fmt.Errorf("Microsoft Graph API request failed: %w", err)
		}

		// Handle 410 Gone or 404 - delta token expired, need full sync
		if resp.StatusCode == http.StatusGone || (resp.StatusCode == http.StatusNotFound && !isFullSync) {
			resp.Body.Close()
			s.log.Warn().Msg("Microsoft delta link expired, falling back to full sync")
			return s.SyncContactsDelta(accessToken, "")
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
				return nil, fmt.Errorf("Microsoft API authentication failed: %s", string(bodyBytes))
			case http.StatusForbidden:
				return nil, fmt.Errorf("Microsoft API access denied: %s", string(bodyBytes))
			case http.StatusTooManyRequests:
				return nil, fmt.Errorf("Microsoft API rate limit exceeded")
			default:
				return nil, fmt.Errorf("Microsoft Graph API error %d: %s", resp.StatusCode, string(bodyBytes))
			}
		}

		// Parse response
		var result msGraphDeltaResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to parse Microsoft API response: %w", err)
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
			Int("page_count", len(result.Value)).
			Int("records_so_far", len(allRecords)).
			Int("deleted_so_far", len(deletedIDs)).
			Msg("Fetched Microsoft contacts page")

		// Check for more pages or final delta link
		if result.NextLink != "" {
			nextLink = result.NextLink
		} else {
			finalDeltaLink = result.DeltaLink
			nextLink = ""
		}
	}

	syncResult := &SyncResult{
		Records:       allRecords,
		DeletedIDs:    deletedIDs,
		NextSyncToken: finalDeltaLink, // Store deltaLink as sync token
		IsFullSync:    isFullSync,
	}

	if isFullSync {
		s.log.Info().
			Int("total_records", len(allRecords)).
			Bool("has_delta_link", finalDeltaLink != "").
			Msg("Microsoft contacts full sync completed")
		return syncResult, nil
	}
	s.log.Info().
		Int("updated_records", len(allRecords)).
		Int("deleted_contacts", len(deletedIDs)).
		Msg("Microsoft contacts incremental sync completed")
	return syncResult, nil
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
