package app

import (
	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/message"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Folder count maintenance for local actions (read flips, moves, deletes).
//
// folders.total_count / unread_count hold the server's whole-mailbox numbers
// (STATUS MESSAGES / UNSEEN, written by the sync paths). Local message rows
// only cover the account's sync window, so recounting them here would replace
// a 7739 with the window's 403 until the next sync. Actions therefore apply
// the delta they caused, computed from the pre-mutation message objects.

// unreadIn counts the unread messages in msgs.
func unreadIn(msgs []*message.Message) int {
	n := 0
	for _, m := range msgs {
		if m != nil && !m.IsRead {
			n++
		}
	}
	return n
}

// readFlipDelta is the signed change in unread count from marking msgs with
// isRead: messages already in that state contribute nothing.
func readFlipDelta(msgs []*message.Message, isRead bool) int {
	flipped := 0
	for _, m := range msgs {
		if m != nil && m.IsRead != isRead {
			flipped++
		}
	}
	if isRead {
		return -flipped
	}
	return flipped
}

// adjustFolderCounts shifts one folder's stored counts and records the new
// unread value in counts for the folders:countsChanged event.
func (a *App) adjustFolderCounts(counts map[string]int, folderID string, totalDelta, unreadDelta int) {
	f, err := a.folderStore.AdjustCounts(folderID, totalDelta, unreadDelta)
	if err != nil {
		log := logging.WithComponent("app")
		log.Error().Err(err).Str("folderID", folderID).Msg("Failed to adjust folder counts")
		return
	}
	counts[folderID] = f.UnreadCount
}

// emitFolderCounts publishes updated unread counts to the sidebar.
func (a *App) emitFolderCounts(counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	wailsRuntime.EventsEmit(a.ctx, "folders:countsChanged", counts)
}
