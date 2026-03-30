package app

import (
	"context"
	"time"

	"github.com/hkdb/aerion/internal/message"
	"github.com/hkdb/aerion/internal/sync"
)

// ============================================================================
// Search API - Exposed to frontend via Wails bindings
// ============================================================================

// SearchConversations searches for conversations in a folder using full-text search
// Returns matching conversations with highlighted text
func (a *App) SearchConversations(accountID, folderID, query string, offset, limit int, filter string) ([]*message.ConversationSearchResult, error) {
	results, _, err := a.messageStore.SearchConversations(folderID, query, offset, limit, filter)
	return results, err
}

// GetSearchCount returns the total count of search results in a folder
func (a *App) GetSearchCount(accountID, folderID, query, filter string) (int, error) {
	_, count, err := a.messageStore.SearchConversations(folderID, query, 0, 0, filter)
	return count, err
}

// SearchAccountConversations searches for conversations across all folders in an account.
func (a *App) SearchAccountConversations(accountID, query string, offset, limit int, filter string) ([]*message.ConversationSearchResult, error) {
	results, _, err := a.messageStore.SearchConversationsByAccount(accountID, query, offset, limit, filter)
	return results, err
}

// GetAccountSearchCount returns the total count of search results across all folders in an account.
func (a *App) GetAccountSearchCount(accountID, query, filter string) (int, error) {
	_, count, err := a.messageStore.SearchConversationsByAccount(accountID, query, 0, 0, filter)
	return count, err
}

// SearchAccountParticipants returns people inferred from an account's message history.
func (a *App) SearchAccountParticipants(accountID, query string, limit int) ([]*message.ParticipantSuggestion, error) {
	return a.messageStore.SearchParticipantSuggestionsByAccount(accountID, query, limit)
}

// SearchAccountConversationsByParticipant searches for conversations involving a specific email address.
func (a *App) SearchAccountConversationsByParticipant(accountID, email string, offset, limit int, filter string) ([]*message.ConversationSearchResult, error) {
	results, _, err := a.messageStore.SearchConversationsByParticipant(accountID, email, offset, limit, filter)
	return results, err
}

// GetAccountParticipantSearchCount returns the total count of conversations involving a specific email address.
func (a *App) GetAccountParticipantSearchCount(accountID, email, filter string) (int, error) {
	_, count, err := a.messageStore.SearchConversationsByParticipant(accountID, email, 0, 0, filter)
	return count, err
}

// SearchUnifiedInbox searches across all inbox folders for all accounts
func (a *App) SearchUnifiedInbox(query string, offset, limit int, filter string) ([]*message.ConversationSearchResult, error) {
	results, _, err := a.messageStore.SearchConversationsUnifiedInbox(query, offset, limit, filter)
	return results, err
}

// GetSearchCountUnifiedInbox returns the total count of search results across all inboxes
func (a *App) GetSearchCountUnifiedInbox(query, filter string) (int, error) {
	_, count, err := a.messageStore.SearchConversationsUnifiedInbox(query, 0, 0, filter)
	return count, err
}

// GetFTSIndexStatus returns the indexing status for a specific folder
func (a *App) GetFTSIndexStatus(folderID string) (*message.FTSIndexStatus, error) {
	return a.ftsIndexer.GetIndexStatus(folderID)
}

// GetFTSIndexStatusAll returns the indexing status for all folders
func (a *App) GetFTSIndexStatusAll() (map[string]*message.FTSIndexStatus, error) {
	return a.ftsIndexer.GetAllIndexStatuses()
}

// IsFTSIndexComplete checks if a folder is fully indexed
func (a *App) IsFTSIndexComplete(folderID string) bool {
	return a.ftsIndexer.IsIndexComplete(folderID)
}

// IsFTSIndexing returns true if any folder is currently being indexed
func (a *App) IsFTSIndexing() bool {
	return a.ftsIndexer.IsAnyIndexing()
}

// RebuildFTSIndex forces a rebuild of the FTS index for a folder
func (a *App) RebuildFTSIndex(folderID string) error {
	return a.ftsIndexer.RebuildIndex(a.ctx, folderID)
}

// IMAPSearchFolder performs a server-side IMAP SEARCH query on a specific folder.
// Returns results with local message data where available, envelope data for non-local messages.
// When limit > 0, only the newest `limit` results are returned but totalCount reflects all matches.
func (a *App) IMAPSearchFolder(accountID, folderID, query string, limit int) (*sync.IMAPSearchResponse, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	return a.syncEngine.IMAPSearch(ctx, accountID, folderID, query, limit)
}

// IMAPSearchFolderByParticipant performs a server-side IMAP SEARCH query in a specific folder
// for a specific participant email.
func (a *App) IMAPSearchFolderByParticipant(accountID, folderID, email string, limit int) (*sync.IMAPSearchResponse, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	return a.syncEngine.IMAPSearchByParticipant(ctx, accountID, folderID, email, limit)
}

// IMAPSearchAccount performs a server-side IMAP SEARCH query across all folders in an account.
func (a *App) IMAPSearchAccount(accountID, query string, limit int) (*sync.IMAPSearchResponse, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	return a.syncEngine.IMAPSearchAccount(ctx, accountID, query, limit)
}

// IMAPSearchAccountByParticipant performs a server-side IMAP SEARCH query across all folders
// in an account for a specific participant email.
func (a *App) IMAPSearchAccountByParticipant(accountID, email string, limit int) (*sync.IMAPSearchResponse, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	return a.syncEngine.IMAPSearchAccountByParticipant(ctx, accountID, email, limit)
}

// FetchServerMessage fetches a full message by UID from the IMAP server, saves it locally,
// and returns it. Used when interacting with non-local server search results.
func (a *App) FetchServerMessage(accountID, folderID string, uid int) (*message.Message, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return a.syncEngine.FetchServerMessage(ctx, accountID, folderID, uint32(uid))
}
