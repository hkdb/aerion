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

// SyncContacts fetches all contacts from Microsoft Graph API (full sync).
// Uses the /me/contacts endpoint with pagination.
// The accessToken should be a valid Microsoft OAuth2 access token with Contacts.Read scope.
func (s *MicrosoftContactsSyncer) SyncContacts(accessToken string) ([]SyncedContact, error) {
	result, err := s.SyncContactsDelta(accessToken, "")
	if err != nil {
		return nil, err
	}
	return result.Contacts, nil
}

// SyncContactsDelta performs an incremental sync using Microsoft Graph delta queries.
// If deltaLink is empty, performs a full sync and returns a deltaLink for future incremental syncs.
// If the deltaLink is expired, automatically falls back to full sync.
func (s *MicrosoftContactsSyncer) SyncContactsDelta(accessToken, deltaLink string) (*SyncResult, error) {
	var allContacts []SyncedContact
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

		// Convert to SyncedContact structs
		for _, contact := range result.Value {
			// Check if this is a deleted contact
			if contact.Removed != nil {
				deletedIDs = append(deletedIDs, contact.ID)
				continue
			}

			if len(contact.EmailAddresses) == 0 {
				continue
			}

			// Create one contact entry per email address
			for _, email := range contact.EmailAddresses {
				if email.Address == "" {
					continue
				}
				allContacts = append(allContacts, SyncedContact{
					Email:       email.Address,
					DisplayName: contact.DisplayName,
					RemoteID:    contact.ID,
				})
			}
		}

		s.log.Debug().
			Int("page_count", len(result.Value)).
			Int("contacts_so_far", len(allContacts)).
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
		Contacts:      allContacts,
		DeletedIDs:    deletedIDs,
		NextSyncToken: finalDeltaLink, // Store deltaLink as sync token
		IsFullSync:    isFullSync,
	}

	if isFullSync {
		s.log.Info().
			Int("total_contacts", len(allContacts)).
			Bool("has_delta_link", finalDeltaLink != "").
			Msg("Microsoft contacts full sync completed")
	} else {
		s.log.Info().
			Int("updated_contacts", len(allContacts)).
			Int("deleted_contacts", len(deletedIDs)).
			Msg("Microsoft contacts incremental sync completed")
	}

	return syncResult, nil
}

// Microsoft Graph API contacts response structures

type msGraphContactsResponse struct {
	Value    []msGraphContact `json:"value"`
	NextLink string           `json:"@odata.nextLink"`
}

// msGraphDeltaResponse is used for delta sync responses
type msGraphDeltaResponse struct {
	Value     []msGraphDeltaContact `json:"value"`
	NextLink  string                `json:"@odata.nextLink"`
	DeltaLink string                `json:"@odata.deltaLink"` // Final link for next incremental sync
}

type msGraphContact struct {
	ID             string         `json:"id"`
	DisplayName    string         `json:"displayName"`
	EmailAddresses []msGraphEmail `json:"emailAddresses"`
}

// msGraphDeltaContact extends msGraphContact with removal info
type msGraphDeltaContact struct {
	ID             string              `json:"id"`
	DisplayName    string              `json:"displayName"`
	EmailAddresses []msGraphEmail      `json:"emailAddresses"`
	Removed        *msGraphRemovedInfo `json:"@removed,omitempty"` // Present when contact was deleted
}

type msGraphRemovedInfo struct {
	Reason string `json:"reason"` // "changed" or "deleted"
}

type msGraphEmail struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}
