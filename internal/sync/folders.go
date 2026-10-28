package sync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	gosync "sync"
	"time"

	"github.com/hkdb/aerion/internal/folder"
	imapPkg "github.com/hkdb/aerion/internal/imap"
)

// folderStatusResult holds the result of a parallel STATUS fetch
type folderStatusResult struct {
	mailbox *imapPkg.Mailbox
	status  *imapPkg.Mailbox // STATUS returns same type as LIST but with counts populated
	err     error
}

// folderStatusWorkers is how many connections the folder stage pipelines
// STATUS over. Two chunks finish nearly as fast as four once each chunk is a
// single round trip, and the stage must leave pool slots for body fetches,
// IDLE follow-ups and user actions (pool cap is 4).
const folderStatusWorkers = 2

// SyncFolders synchronizes the folder list for an account
func (e *Engine) SyncFolders(ctx context.Context, accountID string) error {
	return e.SyncFoldersSkipStatus(ctx, accountID, nil)
}

// FolderPathSet returns the mailbox paths of folders as a set, for
// SyncFoldersSkipStatus.
func FolderPathSet(folders []*folder.Folder) map[string]bool {
	if len(folders) == 0 {
		return nil
	}
	set := make(map[string]bool, len(folders))
	for _, f := range folders {
		if f != nil && f.Path != "" {
			set[f.Path] = true
		}
	}
	return set
}

// SyncFoldersSkipStatus is SyncFolders with a set of mailbox paths whose
// STATUS round trip is skipped. Callers pass the folders the same pass is
// about to message-sync: SyncMessages reads the same numbers from SELECT and
// its own STATUS and overwrites them seconds later, so fetching them here is
// pure duplication. Skipped folders keep their stored counts and sync state,
// are still reconciled (name, type, subscription, parent) and never deleted.
func (e *Engine) SyncFoldersSkipStatus(ctx context.Context, accountID string, skipStatusPaths map[string]bool) error {
	start := time.Now()
	e.log.Debug().Str("account", accountID).Msg("Syncing folders")

	// Get a connection from the pool for LIST. Released explicitly below once
	// LIST/LSUB are done; the closure form keeps the early-error returns safe
	// (Release(nil) is a no-op after the explicit release).
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer func() { e.pool.Release(conn) }()

	// List mailboxes from server
	mailboxes, err := conn.Client().ListMailboxes()
	if err != nil {
		return fmt.Errorf("failed to list mailboxes: %w", err)
	}

	// Fetch subscribed mailbox set (for caching subscription state locally)
	subscribedSet, subErr := conn.Client().ListSubscribedMailboxes()
	if subErr != nil {
		e.log.Info().Err(subErr).Str("account", accountID).Msg("Failed to list subscribed mailboxes, subscription state will not be cached")
	}

	// LIST/LSUB are done; hand the connection back before the STATUS fan-out so
	// the workers below can use every pool slot instead of fighting over the
	// ones this function was pinning (Gmail: ~40 STATUS through 1 free slot,
	// each acquisition paying the pool's NOOP health probe).
	e.pool.Release(conn)
	conn = nil

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
	results := e.fetchFolderStatusParallel(ctx, accountID, mailboxes, skipStatusPaths)
	statusSkipped := 0
	for _, mb := range mailboxes {
		if skipStatusPaths[mb.Name] {
			statusSkipped++
		}
	}

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
	// Unread counts that changed on this pass (folder ID → unread), pushed to
	// the sidebar at the end: a scheduled pass has no tree reload, so without
	// this a folder read on another machine keeps its stale badge here.
	changedCounts := make(map[string]int)

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

		// Skip folders where STATUS failed entirely (or was never attempted).
		// A folder in skipStatusPaths arrives here with nil status AND nil err:
		// it falls through, the `status != nil` guards below leave its stored
		// counts alone, and seenPaths keeps it from being deleted.
		if errors.Is(result.err, errNoSelectMailbox) {
			e.log.Debug().Str("mailbox", mb.Name).Msg("Skipping non-selectable mailbox (\\Noselect)")
			continue
		}
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

			unreadChanged := false
			if status != nil {
				unreadChanged = existing.UnreadCount != int(status.Unseen)
				existing.UIDValidity = status.UIDValidity
				existing.UIDNext = status.UIDNext
				existing.HighestModSeq = status.HighestModSeq
				existing.TotalCount = int(status.Messages)
				existing.UnreadCount = int(status.Unseen)
			}

			if err := e.folderStore.Update(existing); err != nil {
				e.log.Warn().Err(err).Str("path", mb.Name).Msg("Failed to update folder")
			} else if unreadChanged {
				changedCounts[existing.ID] = existing.UnreadCount
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
				if status != nil {
					changedCounts[f.ID] = f.UnreadCount
				}
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

	e.emitFolderCounts(accountID, changedCounts)

	e.log.Info().Str("account", accountID).Int("folders", len(mailboxes)).Int("statusSkipped", statusSkipped).Int("countsChanged", len(changedCounts)).Dur("elapsed", time.Since(start)).Msg("Folder sync complete")

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
		current string
		fType   folder.Type
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

	e.log.Info().Str("account", accountID).Msg("Auto-detected folder mappings persisted")
}

// errNoSelectMailbox marks a LIST entry that carries \Noselect — a hierarchy
// node that can hold child mailboxes but no messages (Gmail's "[Gmail]",
// client-created "[Mailspring]"). STATUS on it is a guaranteed NO, so skip
// the round trip; the caller drops it the same way it drops a failed STATUS.
var errNoSelectMailbox = errors.New("mailbox is not selectable")

// hasNoSelectAttr reports whether a LIST attribute set contains \Noselect.
func hasNoSelectAttr(attrs []string) bool {
	for _, a := range attrs {
		if strings.EqualFold(a, `\Noselect`) {
			return true
		}
	}
	return false
}

// classifyStatusTargets pre-fills the results that need no server round
// trip (\Noselect containers, folders the caller is about to message-sync)
// and returns the indices that still need a STATUS.
func classifyStatusTargets(mailboxes []*imapPkg.Mailbox, skip map[string]bool) ([]folderStatusResult, []int) {
	results := make([]folderStatusResult, len(mailboxes))
	var pending []int
	for i, mb := range mailboxes {
		results[i].mailbox = mb
		switch {
		case hasNoSelectAttr(mb.Attributes):
			results[i].err = errNoSelectMailbox
		case skip[mb.Name]:
			// nil status, nil err: the reconcile loop keeps the stored counts.
		default:
			pending = append(pending, i)
		}
	}
	return results, pending
}

// chunkIndices splits idx into at most n contiguous, near-equal chunks
// (n < 1 is treated as 1 so no index is ever dropped).
func chunkIndices(idx []int, n int) [][]int {
	if len(idx) == 0 {
		return nil
	}
	if n < 1 {
		n = 1
	}
	if n > len(idx) {
		n = len(idx)
	}
	chunks := make([][]int, 0, n)
	size := (len(idx) + n - 1) / n
	for start := 0; start < len(idx); start += size {
		end := start + size
		if end > len(idx) {
			end = len(idx)
		}
		chunks = append(chunks, idx[start:end])
	}
	return chunks
}

// fetchFolderStatusParallel fetches STATUS for the mailboxes that need it.
// The work is split into one chunk per pool worker; each chunk is pipelined
// on a single connection (every STATUS sent before the first reply is read),
// so a folder costs the server's work for it, not a full round trip plus a
// pool acquisition. Any reply that failed is retried once on its own.
func (e *Engine) fetchFolderStatusParallel(ctx context.Context, accountID string, mailboxes []*imapPkg.Mailbox, skip map[string]bool) []folderStatusResult {
	results, pending := classifyStatusTargets(mailboxes, skip)
	if len(pending) == 0 {
		return results
	}

	// Never more workers than the pool can hold for one account — extra
	// workers would only queue on the pool.
	workers := folderStatusWorkers
	if m := e.pool.MaxConnections(); m > 0 && m < workers {
		workers = m
	}

	var wg gosync.WaitGroup
	for _, chunk := range chunkIndices(pending, workers) {
		wg.Add(1)
		go func(idx []int) {
			defer wg.Done()
			e.fetchStatusChunk(ctx, accountID, mailboxes, idx, results)
		}(chunk)
	}
	wg.Wait()
	return results
}

// fetchStatusChunk pipelines STATUS for one chunk on one connection and
// writes the positional results. Failures are retried individually on a
// fresh connection: a decode error on one reply kills the connection and
// fails every reply behind it (go-imap closes on any decode error), and that
// must cost one reconnect, not the whole pass.
func (e *Engine) fetchStatusChunk(ctx context.Context, accountID string, mailboxes []*imapPkg.Mailbox, idx []int, results []folderStatusResult) {
	names := make([]string, len(idx))
	for i, mi := range idx {
		names[i] = mailboxes[mi].Name
	}

	statuses, errs := e.pipelineStatus(ctx, accountID, names)
	for i, mi := range idx {
		results[mi] = folderStatusResult{mailbox: mailboxes[mi], status: statuses[i], err: errs[i]}
	}

	for i, mi := range idx {
		if errs[i] == nil || ctx.Err() != nil {
			continue
		}
		e.log.Debug().Err(errs[i]).Str("mailbox", names[i]).Msg("Pipelined STATUS failed, retrying individually")
		status, err := e.fetchStatusSingle(ctx, accountID, names[i])
		results[mi] = folderStatusResult{mailbox: mailboxes[mi], status: status, err: err}
	}
}

// pipelineStatus runs one pipelined STATUS batch on a pooled connection.
func (e *Engine) pipelineStatus(ctx context.Context, accountID string, names []string) ([]*imapPkg.Mailbox, []error) {
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		errs := make([]error, len(names))
		for i := range errs {
			errs[i] = err
		}
		return make([]*imapPkg.Mailbox, len(names)), errs
	}
	defer e.pool.Release(conn)
	return conn.Client().GetMailboxStatuses(ctx, names)
}

// fetchStatusSingle runs one STATUS on its own pooled connection.
func (e *Engine) fetchStatusSingle(ctx context.Context, accountID string, name string) (*imapPkg.Mailbox, error) {
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer e.pool.Release(conn)
	return conn.Client().GetMailboxStatus(ctx, name)
}
