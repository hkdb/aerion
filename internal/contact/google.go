// Package contact provides contact management for email autocomplete
package contact

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// GoogleContactsClient queries Google People API for contact autocomplete.
// It searches the user's "other contacts" (people the user has interacted with)
// using the contacts.other.readonly scope.
type GoogleContactsClient struct {
	httpClient *http.Client
	cache      map[string]cachedGoogleResult
	cacheMu    sync.RWMutex
	cacheTTL   time.Duration
	log        zerolog.Logger
}

type cachedGoogleResult struct {
	contacts  []*Contact
	expiresAt time.Time
}

// NewGoogleContactsClient creates a new Google People API client.
// Results are cached for 15 minutes by default.
func NewGoogleContactsClient() *GoogleContactsClient {
	return &GoogleContactsClient{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		cache:      make(map[string]cachedGoogleResult),
		cacheTTL:   15 * time.Minute,
		log:        logging.WithComponent("google-contacts"),
	}
}

// Search queries Google People API for contacts matching the query.
// Uses the otherContacts:search endpoint which requires the
// contacts.other.readonly scope.
//
// The accessToken should be a valid Google OAuth2 access token.
func (c *GoogleContactsClient) Search(accessToken, query string, limit int) ([]*Contact, error) {
	if query == "" {
		return nil, nil
	}

	if limit <= 0 {
		limit = 10
	}
	// Google API limits pageSize to 30
	if limit > 30 {
		limit = 30
	}

	// Check cache first (key includes first few chars of token to separate users)
	cacheKey := c.cacheKey(accessToken, query)
	c.cacheMu.RLock()
	if cached, ok := c.cache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		c.cacheMu.RUnlock()
		c.log.Debug().Str("query", query).Int("cached_count", len(cached.contacts)).Msg("Returning cached Google contacts")
		return cached.contacts, nil
	}
	c.cacheMu.RUnlock()

	// Query Google People API - otherContacts:search
	// This searches contacts the user has interacted with but hasn't explicitly saved
	apiURL := fmt.Sprintf(
		"https://people.googleapis.com/v1/otherContacts:search?query=%s&readMask=names,emailAddresses&pageSize=%d",
		url.QueryEscape(query),
		limit,
	)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	c.log.Debug().Str("query", query).Int("limit", limit).Msg("Searching Google contacts")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.log.Warn().Err(err).Msg("Google People API request failed")
		return nil, fmt.Errorf("Google People API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Handle specific error codes
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("Google API authentication failed (token may be expired)")
		case http.StatusForbidden:
			c.log.Warn().Int("status", resp.StatusCode).Msg("Google People API access denied (scope may not be granted)")
			// Return empty results instead of error - user may not have granted contacts scope
			return []*Contact{}, nil
		case http.StatusTooManyRequests:
			c.log.Warn().Msg("Google People API rate limited")
			return nil, fmt.Errorf("Google API rate limit exceeded")
		default:
			return nil, fmt.Errorf("Google People API error: %d", resp.StatusCode)
		}
	}

	// Parse response
	var result googleSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse Google API response: %w", err)
	}

	// Convert to Contact structs
	contacts := make([]*Contact, 0, len(result.Results))
	for _, r := range result.Results {
		if len(r.Person.EmailAddresses) == 0 {
			continue
		}

		name := ""
		if len(r.Person.Names) > 0 {
			name = r.Person.Names[0].DisplayName
		}

		for _, email := range r.Person.EmailAddresses {
			if email.Value == "" {
				continue
			}
			contacts = append(contacts, &Contact{
				Email:       email.Value,
				DisplayName: name,
				Source:      "google",
			})
		}
	}

	c.log.Debug().
		Str("query", query).
		Int("result_count", len(contacts)).
		Msg("Google contacts search completed")

	// Cache results
	c.cacheMu.Lock()
	c.cache[cacheKey] = cachedGoogleResult{
		contacts:  contacts,
		expiresAt: time.Now().Add(c.cacheTTL),
	}
	c.cacheMu.Unlock()

	return contacts, nil
}

// cacheKey generates a cache key from the access token and query.
// Uses last 8 chars of token to differentiate users without storing full token.
func (c *GoogleContactsClient) cacheKey(accessToken, query string) string {
	// Use last 8 chars of token as user identifier
	tokenSuffix := ""
	if len(accessToken) >= 8 {
		tokenSuffix = accessToken[len(accessToken)-8:]
	}
	return tokenSuffix + ":" + query
}

// ClearCache clears all cached results.
// Useful when user re-authenticates or logs out.
func (c *GoogleContactsClient) ClearCache() {
	c.cacheMu.Lock()
	c.cache = make(map[string]cachedGoogleResult)
	c.cacheMu.Unlock()
	c.log.Debug().Msg("Cleared Google contacts cache")
}

// ClearExpiredCache removes expired entries from the cache.
// Called periodically to prevent memory growth.
func (c *GoogleContactsClient) ClearExpiredCache() {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	now := time.Now()
	for key, cached := range c.cache {
		if now.After(cached.expiresAt) {
			delete(c.cache, key)
		}
	}
}

// Google People API response structures

type googleSearchResponse struct {
	Results []googleSearchResult `json:"results"`
}

type googleSearchResult struct {
	Person googlePerson `json:"person"`
}

type googlePerson struct {
	Names          []googleName  `json:"names"`
	EmailAddresses []googleEmail `json:"emailAddresses"`
}

type googleName struct {
	DisplayName string `json:"displayName"`
	GivenName   string `json:"givenName"`
	FamilyName  string `json:"familyName"`
}

type googleEmail struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}
