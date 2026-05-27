package sync

import (
	"context"
	"fmt"
	"sort"
	"strings"
	gosync "sync"

	"github.com/hkdb/aerion/internal/folder"
	imapPkg "github.com/hkdb/aerion/internal/imap"
)

// folderStatusResult holds the result of a parallel STATUS fetch
type folderStatusResult struct {
	mailbox *imapPkg.Mailbox
	status  *imapPkg.Mailbox // STATUS returns same type as LIST but with counts populated
	err     error
}

// Concurrency limit for parallel STATUS fetches
const folderStatusWorkers = 5

// SyncFolders synchronizes the folder list for an account
func (e *Engine) SyncFolders(ctx context.Context, accountID string) error {
	e.log.Debug().Str("account", accountID).Msg("Syncing folders")

	// Get a connection from the pool for LIST
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer e.pool.Release(conn)

	// List mailboxes from server
	mailboxes, err := conn.Client().ListMailboxes()
	if err != nil {
		return fmt.Errorf("failed to list mailboxes: %w", err)
	}

	// Fetch subscribed mailbox set (for caching subscription state locally)
	subscribedSet, subErr := conn.Client().ListSubscribedMailboxes()
	if subErr != nil {
		e.log.Debug().Err(subErr).Str("account", accountID).Msg("Failed to list subscribed mailboxes, subscription state will not be cached")
	}

	totalFolders := len(mailboxes)
	if totalFolders == 0 {
		e.log.Info().Str("account", accountID).Msg("No folders found")
		return nil
	}

	// Emit initial "folders" phase progress
	e.emitProgress(accountID, "", 0, totalFolders, "folders")

	// Get existing local folders
	localFolders, err := e.folderStore.List(accountID)
	if err != nil {
		return fmt.Errorf("failed to list local folders: %w", err)
	}

	// Build a map of local folders by path
	localByPath := make(map[string]*folder.Folder)
	for _, f := range localFolders {
		localByPath[f.Path] = f
	}

	// Build path→type override map from account folder mappings
	// This ensures user-configured folder mappings take precedence over IMAP auto-detection
	pathTypeOverrides := make(map[string]folder.Type)
	if acc, err := e.accountStore.Get(accountID); err == nil && acc != nil {
		mappings := map[string]folder.Type{
			acc.SentFolderPath:    folder.TypeSent,
			acc.DraftsFolderPath:  folder.TypeDrafts,
			acc.TrashFolderPath:   folder.TypeTrash,
			acc.SpamFolderPath:    folder.TypeSpam,
			acc.ArchiveFolderPath: folder.TypeArchive,
			acc.AllMailFolderPath: folder.TypeAll,
			acc.StarredFolderPath: folder.TypeStarred,
		}
		for path, fType := range mappings {
			if path != "" {
				pathTypeOverrides[path] = fType
			}
		}
	}

	// Fetch STATUS for all folders in parallel
	results := e.fetchFolderStatusParallel(ctx, accountID, mailboxes)

	// Sort results by path depth so parents are processed before children.
	// IMAP LIST does not guarantee ordering, so a child may appear before its parent.
	sort.SliceStable(results, func(i, j int) bool {
		iDelim := results[i].mailbox.Delimiter
		jDelim := results[j].mailbox.Delimiter
		iDepth := 1
		jDepth := 1
		if iDelim != "" {
			iDepth = strings.Count(results[i].mailbox.Name, iDelim) + 1
		}
		if jDelim != "" {
			jDepth = strings.Count(results[j].mailbox.Name, jDelim) + 1
		}
		return iDepth < jDepth
	})

	// Track which paths we've seen and process results
	seenPaths := make(map[string]bool)
	processed := 0

	for _, result := range results {
		mb := result.mailbox
		seenPaths[mb.Name] = true
		processed++

		// Emit progress
		e.emitProgress(accountID, "", processed, totalFolders, "folders")

		// Convert IMAP folder type to our folder type
		folderType := convertFolderType(mb.Type)

		// Override with account folder mapping if configured
		if override, ok := pathTypeOverrides[mb.Name]; ok {
			folderType = override
		}

		// Skip folders where STATUS failed entirely
		if result.err != nil && result.status == nil {
			e.log.Debug().Err(result.err).Str("mailbox", mb.Name).Msg("Failed to get mailbox status, skipping folder")
			continue
		}

		status := result.status

		// Update subscription state from server
		isSubscribed := subscribedSet != nil && subscribedSet[mb.Name]

		// Check if folder exists locally
		if existing, ok := localByPath[mb.Name]; ok {
			// Update existing folder
			existing.Name = extractFolderName(mb.Name, mb.Delimiter)
			existing.Type = folderType
			// Only update subscription state when the server returned data.
			// Some servers (Microsoft 365) reject LIST-EXTENDED (SUBSCRIBED);
			// when subscribedSet is nil, preserve local state set by the user
			// via SubscribeFolder/UnsubscribeFolder rather than wiping it.
			if subscribedSet != nil {
				existing.Subscribed = isSubscribed
			}

			// Recompute parent (self-healing for broken parent links)
			if mb.Delimiter != "" {
				parts := strings.Split(mb.Name, mb.Delimiter)
				if len(parts) > 1 {
					parentPath := strings.Join(parts[:len(parts)-1], mb.Delimiter)
					if parent, ok := localByPath[parentPath]; ok {
						existing.ParentID = parent.ID
					}
				}
			}

			if status != nil {
				existing.UIDValidity = status.UIDValidity
				existing.UIDNext = status.UIDNext
				existing.HighestModSeq = status.HighestModSeq
				existing.TotalCount = int(status.Messages)
				existing.UnreadCount = int(status.Unseen)
			}

			if err := e.folderStore.Update(existing); err != nil {
				e.log.Warn().Err(err).Str("path", mb.Name).Msg("Failed to update folder")
			}
		} else {
			// Create new folder
			f := &folder.Folder{
				AccountID:  accountID,
				Name:       extractFolderName(mb.Name, mb.Delimiter),
				Path:       mb.Name,
				Type:       folderType,
				Subscribed: isSubscribed,
			}
			if status != nil {
				f.UIDValidity = status.UIDValidity
				f.UIDNext = status.UIDNext
				f.HighestModSeq = status.HighestModSeq
				f.TotalCount = int(status.Messages)
				f.UnreadCount = int(status.Unseen)
			}

			// Handle parent folder
			if mb.Delimiter != "" {
				parts := strings.Split(mb.Name, mb.Delimiter)
				if len(parts) > 1 {
					parentPath := strings.Join(parts[:len(parts)-1], mb.Delimiter)
					if parent, ok := localByPath[parentPath]; ok {
						f.ParentID = parent.ID
					}
				}
			}

			if err := e.folderStore.Create(f); err != nil {
				e.log.Warn().Err(err).Str("path", mb.Name).Msg("Failed to create folder")
			} else {
				// Add to local map so child folders can find their parent
				localByPath[f.Path] = f
			}
		}
	}

	// Delete folders that no longer exist on server
	for path, f := range localByPath {
		if !seenPaths[path] {
			e.log.Debug().Str("path", path).Msg("Deleting removed folder")
			if err := e.folderStore.Delete(f.ID); err != nil {
				e.log.Warn().Err(err).Str("path", path).Msg("Failed to delete folder")
			}
		}
	}

	e.log.Info().Str("account", accountID).Int("folders", len(mailboxes)).Msg("Folder sync complete")

	// Persist auto-detected folder mappings if not already set.
	// This ensures getSpecialFolder always finds the correct folder via the
	// account mapping, even when multiple folders match the same type by name
	// (e.g., iCloud's "Sent" and "Sent Messages" both match TypeSent).
	e.persistAutoDetectedMappings(accountID)

	return nil
}

// persistAutoDetectedMappings checks if the account's folder path mappings are
// empty and fills them from auto-detected folder types. Only writes to DB when
// at least one mapping was empty and a detected folder was found.
func (e *Engine) persistAutoDetectedMappings(accountID string) {
	acc, err := e.accountStore.Get(accountID)
	if err != nil || acc == nil {
		return
	}

	type mapping struct {
		current  string
		fType    folder.Type
	}

	mappings := []mapping{
		{acc.SentFolderPath, folder.TypeSent},
		{acc.DraftsFolderPath, folder.TypeDrafts},
		{acc.TrashFolderPath, folder.TypeTrash},
		{acc.SpamFolderPath, folder.TypeSpam},
		{acc.ArchiveFolderPath, folder.TypeArchive},
		{acc.AllMailFolderPath, folder.TypeAll},
		{acc.StarredFolderPath, folder.TypeStarred},
	}

	results := make([]string, len(mappings))
	updated := false

	for i, m := range mappings {
		results[i] = m.current
		if m.current != "" {
			continue
		}
		detected, _ := e.folderStore.GetByType(accountID, m.fType)
		if detected != nil {
			results[i] = detected.Path
			updated = true
		}
	}

	if !updated {
		return
	}

	if err := e.accountStore.UpdateFolderMappings(
		accountID,
		results[0], results[1], results[2], results[3], results[4], results[5], results[6],
	); err != nil {
		e.log.Warn().Err(err).Str("account", accountID).Msg("Failed to persist auto-detected folder mappings")
		return
	}

	e.log.Debug().Str("account", accountID).Msg("Auto-detected folder mappings persisted")
}

// fetchFolderStatusParallel fetches STATUS for multiple folders concurrently
func (e *Engine) fetchFolderStatusParallel(ctx context.Context, accountID string, mailboxes []*imapPkg.Mailbox) []folderStatusResult {
	results := make([]folderStatusResult, len(mailboxes))

	// Use a semaphore to limit concurrency
	sem := make(chan struct{}, folderStatusWorkers)
	var wg gosync.WaitGroup

	for i, mb := range mailboxes {
		wg.Add(1)
		go func(idx int, mailbox *imapPkg.Mailbox) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[idx] = folderStatusResult{mailbox: mailbox, err: ctx.Err()}
				return
			}

			// Get a connection for this STATUS request
			conn, err := e.pool.GetConnection(ctx, accountID)
			if err != nil {
				results[idx] = folderStatusResult{mailbox: mailbox, err: err}
				return
			}
			defer e.pool.Release(conn)

			// Fetch STATUS
			status, err := conn.Client().GetMailboxStatus(ctx, mailbox.Name)
			results[idx] = folderStatusResult{
				mailbox: mailbox,
				status:  status,
				err:     err,
			}
		}(i, mb)
	}

	wg.Wait()
	return results
}
