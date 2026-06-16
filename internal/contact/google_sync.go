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

// SyncedRecord is the rich, multi-field payload a sync source emits per remote
// contact. It carries a full *Record (name, emails, phones, addresses, org,
// title) so the storage layer can land it on the shared record path —
// crucially, a contact with NO email is still a valid SyncedRecord. RemoteID is
// the provider id (used as the record's href for change detection); ETag is the
// provider's change tag (empty for providers that don't expose one).
type SyncedRecord struct {
	Record   *Record
	RemoteID string
	ETag     string
}

// SyncResult represents the result of an incremental sync
type SyncResult struct {
	Records       []SyncedRecord // New or updated contacts (full record, email optional)
	DeletedIDs    []string       // Remote IDs of deleted contacts
	NextSyncToken string         // Token for next incremental sync
	IsFullSync    bool           // True if this was a full sync (no valid token)
}

// GoogleContactsSyncer syncs contacts from Google People API.
// Uses the people.connections endpoint to fetch all user's saved contacts.
type GoogleContactsSyncer struct {
	httpClient *http.Client
	log        zerolog.Logger
}

// NewGoogleContactsSyncer creates a new Google contacts syncer.
func NewGoogleContactsSyncer() *GoogleContactsSyncer {
	return &GoogleContactsSyncer{
		httpClient: &http.Client{Timeout: 60 * time.Second}, // Longer timeout for sync
		log:        logging.WithComponent("google-contacts-sync"),
	}
}

// SyncContactsDelta performs an incremental sync using Google's syncToken mechanism.
// If syncToken is empty, performs a full sync and returns a token for future incremental syncs.
// If the syncToken is expired (410 Gone), automatically falls back to full sync.
func (s *GoogleContactsSyncer) SyncContactsDelta(accessToken, syncToken string) (*SyncResult, error) {
	var allRecords []SyncedRecord
	var deletedIDs []string
	pageToken := ""
	isFullSync := syncToken == ""
	requestSync := true // Whether to request a sync token in response

	if isFullSync {
		s.log.Info().Msg("Starting Google contacts full sync")
	} else {
		s.log.Info().Msg("Starting Google contacts incremental sync")
	}

	for {
		// Build API URL with pagination and sync token
		apiURL := "https://people.googleapis.com/v1/people/me/connections?personFields=names,emailAddresses,phoneNumbers,addresses,organizations&pageSize=1000"
		if pageToken != "" {
			apiURL += "&pageToken=" + pageToken
		}
		if syncToken != "" && pageToken == "" {
			// Only include syncToken on first request (not pagination requests)
			apiURL += "&syncToken=" + syncToken
		}
		if requestSync {
			apiURL += "&requestSyncToken=true"
		}

		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			s.log.Error().Err(err).Msg("Google People API request failed")
			return nil, fmt.Errorf("Google People API request failed: %w", err)
		}

		// Handle 410 Gone - sync token expired, need full sync
		if resp.StatusCode == http.StatusGone {
			resp.Body.Close()
			if !isFullSync {
				s.log.Warn().Msg("Google sync token expired, falling back to full sync")
				return s.SyncContactsDelta(accessToken, "")
			}
			return nil, fmt.Errorf("Google API returned 410 Gone on full sync")
		}

		if resp.StatusCode != http.StatusOK {
			// Read the error response body for error handling
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			// Check for expired sync token in 400 Bad Request
			if resp.StatusCode == http.StatusBadRequest {
				var errorResp googleErrorResponse
				if err := json.Unmarshal(bodyBytes, &errorResp); err == nil {
					// Check if this is an EXPIRED_SYNC_TOKEN error
					for _, detail := range errorResp.Error.Details {
						if detail.Reason == "EXPIRED_SYNC_TOKEN" {
							if !isFullSync {
								s.log.Warn().Msg("Google sync token expired (400 EXPIRED_SYNC_TOKEN), falling back to full sync")
								return s.SyncContactsDelta(accessToken, "")
							}
							return nil, fmt.Errorf("Google API returned EXPIRED_SYNC_TOKEN on full sync")
						}
					}
				}
			}

			s.log.Error().
				Int("status", resp.StatusCode).
				Msg("Google People API error response")

			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return nil, fmt.Errorf("Google API authentication failed (token may be expired): %s", string(bodyBytes))
			case http.StatusForbidden:
				return nil, fmt.Errorf("Google API access denied: %s", string(bodyBytes))
			case http.StatusTooManyRequests:
				return nil, fmt.Errorf("Google API rate limit exceeded")
			default:
				return nil, fmt.Errorf("Google People API error %d: %s", resp.StatusCode, string(bodyBytes))
			}
		}

		// Parse response
		var result googleConnectionsResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to parse Google API response: %w", err)
		}
		resp.Body.Close()

		// Convert to full records (email optional — a phone-only contact is
		// still a valid record).
		for _, conn := range result.Connections {
			// Check if this is a deleted contact (incremental sync only)
			if conn.Metadata != nil && conn.Metadata.Deleted {
				deletedIDs = append(deletedIDs, conn.ResourceName)
				continue
			}
			rec := googleConnToRecord(conn)
			if rec == nil {
				continue
			}
			allRecords = append(allRecords, SyncedRecord{
				Record:   rec,
				RemoteID: conn.ResourceName, // e.g., "people/c12345"
			})
		}

		s.log.Debug().
			Int("page_count", len(result.Connections)).
			Int("records_so_far", len(allRecords)).
			Int("deleted_so_far", len(deletedIDs)).
			Msg("Fetched Google contacts page")

		// Check for more pages
		if result.NextPageToken == "" {
			// Store the next sync token for future incremental syncs
			syncResult := &SyncResult{
				Records:       allRecords,
				DeletedIDs:    deletedIDs,
				NextSyncToken: result.NextSyncToken,
				IsFullSync:    isFullSync,
			}

			if isFullSync {
				s.log.Info().
					Int("total_records", len(allRecords)).
					Bool("has_sync_token", result.NextSyncToken != "").
					Msg("Google contacts full sync completed")
				return syncResult, nil
			}
			s.log.Info().
				Int("updated_records", len(allRecords)).
				Int("deleted_contacts", len(deletedIDs)).
				Msg("Google contacts incremental sync completed")
			return syncResult, nil
		}
		pageToken = result.NextPageToken
	}
}

// Google People API connections response structures

type googleConnectionsResponse struct {
	Connections   []googleConnection `json:"connections"`
	NextPageToken string             `json:"nextPageToken"`
	NextSyncToken string             `json:"nextSyncToken"` // For incremental sync
	TotalPeople   int                `json:"totalPeople"`
	TotalItems    int                `json:"totalItems"`
}

type googleConnection struct {
	ResourceName   string                    `json:"resourceName"` // e.g., "people/c12345"
	Names          []googleName              `json:"names"`
	EmailAddresses []googleEmail             `json:"emailAddresses"`
	PhoneNumbers   []googlePhone             `json:"phoneNumbers"`
	Addresses      []googleAddress           `json:"addresses"`
	Organizations  []googleOrganization      `json:"organizations"`
	Metadata       *googleConnectionMetadata `json:"metadata,omitempty"` // For detecting deleted contacts
}

type googlePhone struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type googleAddress struct {
	StreetAddress string `json:"streetAddress"`
	City          string `json:"city"`
	Region        string `json:"region"`
	PostalCode    string `json:"postalCode"`
	Country       string `json:"country"`
	Type          string `json:"type"`
}

type googleOrganization struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// googleConnToRecord maps a People API connection into the shared multi-field
// Record. Email is optional. Returns nil only when the connection carries no
// name, email, or phone (would be an empty row).
func googleConnToRecord(conn googleConnection) *Record {
	rec := &Record{Source: "carddav"}
	if len(conn.Names) > 0 {
		rec.Fn = conn.Names[0].DisplayName
		rec.NGiven = conn.Names[0].GivenName
		rec.NFamily = conn.Names[0].FamilyName
	}
	if len(conn.Organizations) > 0 {
		rec.Org = conn.Organizations[0].Name
		rec.Title = conn.Organizations[0].Title
	}
	for _, e := range conn.EmailAddresses {
		if e.Value == "" {
			continue
		}
		rec.Emails = append(rec.Emails, RecordEmail{Email: e.Value, EmailType: e.Type})
	}
	for _, p := range conn.PhoneNumbers {
		if p.Value == "" {
			continue
		}
		rec.Phones = append(rec.Phones, RecordPhone{Number: p.Value, PhoneType: p.Type})
	}
	for _, a := range conn.Addresses {
		if a.StreetAddress == "" && a.City == "" && a.Region == "" && a.PostalCode == "" && a.Country == "" {
			continue
		}
		rec.Addresses = append(rec.Addresses, RecordAddress{
			AddrType: a.Type,
			Street:   a.StreetAddress,
			City:     a.City,
			Region:   a.Region,
			Postcode: a.PostalCode,
			Country:  a.Country,
		})
	}
	if rec.Fn == "" && len(rec.Emails) == 0 && len(rec.Phones) == 0 {
		return nil
	}
	return rec
}

type googleConnectionMetadata struct {
	Deleted bool `json:"deleted"` // True if contact was deleted (in incremental sync)
}

// Google API error response structures
type googleErrorResponse struct {
	Error googleErrorDetails `json:"error"`
}

type googleErrorDetails struct {
	Code    int                 `json:"code"`
	Message string              `json:"message"`
	Status  string              `json:"status"`
	Details []googleErrorDetail `json:"details"`
}

type googleErrorDetail struct {
	Type   string `json:"@type"`
	Reason string `json:"reason"` // e.g., "EXPIRED_SYNC_TOKEN"
	Domain string `json:"domain"`
}
