package appstate

// UIState represents the persisted UI state across sessions
type UIState struct {
	// View state (what folder/view is shown in sidebar)
	SelectedAccountID  string `json:"selectedAccountId"`  // 'unified' or real account ID
	SelectedFolderID   string `json:"selectedFolderId"`   // 'inbox' (virtual) or real folder ID
	SelectedFolderName string `json:"selectedFolderName"` // Display name
	SelectedFolderType string `json:"selectedFolderType"` // inbox, sent, drafts, etc.

	// Conversation state (what's shown in viewer)
	SelectedThreadID              string `json:"selectedThreadId"`
	SelectedConversationAccountID string `json:"selectedConversationAccountId"` // Real account ID
	SelectedConversationFolderID  string `json:"selectedConversationFolderId"`  // Real folder ID

	// Pane widths
	SidebarWidth int `json:"sidebarWidth"`
	ListWidth    int `json:"listWidth"`

	// Sidebar section expand/collapse states
	ExpandedAccounts     map[string]bool `json:"expandedAccounts"`     // accountID -> isExpanded (default: true)
	UnifiedInboxExpanded bool            `json:"unifiedInboxExpanded"` // Unified Inbox section (default: true)
	CollapsedFolders     map[string]bool `json:"collapsedFolders"`     // folderID -> isCollapsed (default: false/absent)

	// Active extension pane: "mail" (default) or an extension id like "contacts".
	// The frontend uses this to decide whether to render Mail or an extension's
	// pane after the rail is shown. Missing field -> Mail.
	ActiveExtension string `json:"activeExtension,omitempty"`
}
