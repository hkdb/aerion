// Package contact provides contact management for email autocomplete
package contact

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-vcard"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// VCardScanner scans filesystem paths for vCard files and caches contacts
type VCardScanner struct {
	knownPaths []string      // Paths to scan for .vcf files
	cache      []*Contact    // Cached contacts from vCard files
	cacheTime  time.Time     // When the cache was last updated
	cacheTTL   time.Duration // How long the cache is valid
	mu         sync.RWMutex  // Protects cache access
	log        zerolog.Logger
}

// DefaultVCardPaths returns the default paths to scan for vCard files on Linux
func DefaultVCardPaths() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	return []string{
		// GNOME/Evolution
		filepath.Join(homeDir, ".local", "share", "evolution", "addressbook"),
		// GNOME Contacts
		filepath.Join(homeDir, ".local", "share", "gnome-contacts"),
		// Generic
		filepath.Join(homeDir, ".local", "share", "contacts"),
		// Some apps
		filepath.Join(homeDir, ".contacts"),
		// KDE
		filepath.Join(homeDir, ".local", "share", "kaddressbook"),
		// Older KDE
		filepath.Join(homeDir, ".kde", "share", "apps", "kabc"),
	}
}

// NewVCardScanner creates a new VCardScanner with the given paths and TTL
func NewVCardScanner(paths []string, ttl time.Duration) *VCardScanner {
	return &VCardScanner{
		knownPaths: paths,
		cacheTTL:   ttl,
		log:        logging.WithComponent("vcard"),
	}
}

// IsCacheValid returns true if the cache is still valid (not expired)
func (s *VCardScanner) IsCacheValid() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.cache == nil {
		return false
	}
	return time.Since(s.cacheTime) < s.cacheTTL
}

// CacheSize returns the number of contacts in the cache
func (s *VCardScanner) CacheSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.cache)
}

// Scan scans all known paths for vCard files and updates the cache
// Returns the list of contacts found
func (s *VCardScanner) Scan() ([]*Contact, error) {
	s.log.Info().Strs("paths", s.knownPaths).Msg("Scanning for vCard files")

	var allContacts []*Contact
	seenEmails := make(map[string]bool)

	for _, basePath := range s.knownPaths {
		// Check if path exists
		info, err := os.Stat(basePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // Path doesn't exist, skip
			}
			s.log.Warn().Err(err).Str("path", basePath).Msg("Failed to stat path")
			continue
		}

		if !info.IsDir() {
			// Single file
			if strings.HasSuffix(strings.ToLower(basePath), ".vcf") {
				contacts, err := s.parseVCardFile(basePath)
				if err != nil {
					s.log.Warn().Err(err).Str("path", basePath).Msg("Failed to parse vCard file")
					continue
				}
				for _, c := range contacts {
					email := strings.ToLower(c.Email)
					if !seenEmails[email] {
						seenEmails[email] = true
						allContacts = append(allContacts, c)
					}
				}
			}
			continue
		}

		// Walk directory tree looking for .vcf files
		err = filepath.WalkDir(basePath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // Skip paths we can't access
			}

			if d.IsDir() {
				return nil // Continue into directories
			}

			if !strings.HasSuffix(strings.ToLower(d.Name()), ".vcf") {
				return nil // Not a vCard file
			}

			contacts, err := s.parseVCardFile(path)
			if err != nil {
				s.log.Warn().Err(err).Str("path", path).Msg("Failed to parse vCard file")
				return nil // Continue scanning
			}

			for _, c := range contacts {
				email := strings.ToLower(c.Email)
				if !seenEmails[email] {
					seenEmails[email] = true
					allContacts = append(allContacts, c)
				}
			}

			return nil
		})

		if err != nil {
			s.log.Warn().Err(err).Str("path", basePath).Msg("Error walking directory")
		}
	}

	// Update cache
	s.mu.Lock()
	s.cache = allContacts
	s.cacheTime = time.Now()
	s.mu.Unlock()

	s.log.Info().Int("count", len(allContacts)).Msg("vCard scan complete")
	return allContacts, nil
}

// parseVCardFile parses a single .vcf file and returns contacts
// A single .vcf file may contain multiple vCard entries
func (s *VCardScanner) parseVCardFile(path string) ([]*Contact, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	dec := vcard.NewDecoder(file)
	var contacts []*Contact

	for {
		card, err := dec.Decode()
		if err != nil {
			break // End of file or error
		}

		// Extract name - prefer FN (Formatted Name), fall back to N (Structured Name)
		name := card.PreferredValue(vcard.FieldFormattedName)
		if name == "" {
			// Try to build from structured name
			if n := card.Name(); n != nil {
				parts := []string{}
				if n.GivenName != "" {
					parts = append(parts, n.GivenName)
				}
				if n.FamilyName != "" {
					parts = append(parts, n.FamilyName)
				}
				name = strings.Join(parts, " ")
			}
		}

		// Extract all email addresses
		emails := card.Values(vcard.FieldEmail)
		for _, email := range emails {
			email = strings.TrimSpace(email)
			if email == "" {
				continue
			}

			contacts = append(contacts, &Contact{
				Email:       email,
				DisplayName: name,
				Source:      "vcard",
				SendCount:   0, // vCard contacts have no send history
			})
		}
	}

	return contacts, nil
}

// Search searches the cache for contacts matching the query
// If the cache is expired or has few results, it triggers a live scan
func (s *VCardScanner) Search(query string, limit int) ([]*Contact, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}

	if limit <= 0 {
		limit = 10
	}

	// Check if we need to refresh the cache
	if !s.IsCacheValid() {
		// Do a live scan
		if _, err := s.Scan(); err != nil {
			s.log.Warn().Err(err).Msg("Failed to scan vCard files")
			// Continue with stale cache if available
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Contact
	for _, c := range s.cache {
		// Match against email or display name
		emailLower := strings.ToLower(c.Email)
		nameLower := strings.ToLower(c.DisplayName)

		if strings.Contains(emailLower, query) || strings.Contains(nameLower, query) {
			results = append(results, c)
			if len(results) >= limit {
				break
			}
		}
	}

	return results, nil
}

// GetCachedContacts returns all cached contacts (for merging with other sources)
// Does not trigger a scan - returns whatever is in the cache
func (s *VCardScanner) GetCachedContacts() []*Contact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.cache == nil {
		return nil
	}

	// Return a copy to prevent external modification
	result := make([]*Contact, len(s.cache))
	copy(result, s.cache)
	return result
}

// RefreshIfNeeded checks if the cache is expired and triggers a scan if needed
// This is meant to be called periodically or before searches
func (s *VCardScanner) RefreshIfNeeded() {
	if !s.IsCacheValid() {
		go func() {
			if _, err := s.Scan(); err != nil {
				s.log.Warn().Err(err).Msg("Background vCard scan failed")
			}
		}()
	}
}
