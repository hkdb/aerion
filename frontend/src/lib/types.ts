// Account types
export interface Account {
  id: string;
  name: string;
  email: string;
  folders: Folder[];
}

export interface Folder {
  id: string;
  name: string;
  type: FolderType;
  unread: number;
  total?: number;
  children?: Folder[];
}

export type FolderType =
  | "inbox"
  | "sent"
  | "drafts"
  | "trash"
  | "archive"
  | "spam"
  | "folder";

// Message types
export interface EmailAddress {
  name: string;
  email: string;
}

export interface MessageHeader {
  id: string;
  from: EmailAddress;
  subject: string;
  snippet: string;
  date: Date;
  unread: boolean;
  starred: boolean;
  hasAttachment: boolean;
}

export interface Message extends MessageHeader {
  to: EmailAddress[];
  cc?: EmailAddress[];
  bcc?: EmailAddress[];
  body: string;
  bodyHtml?: string;
  attachments: Attachment[];
  replyTo?: EmailAddress;
  inReplyTo?: string;
  references?: string[];
}

export interface Attachment {
  id: string;
  name: string;
  size: number;
  type: string;
  contentId?: string;
  isInline?: boolean;
}

// Composer types
export interface ComposerState {
  to: EmailAddress[];
  cc: EmailAddress[];
  bcc: EmailAddress[];
  subject: string;
  body: string;
  attachments: File[];
  inReplyTo?: string;
  identityId?: string;
}

// Search types
export interface SearchResult {
  messageId: string;
  accountId: string;
  subject: string;
  from: EmailAddress;
  date: Date;
  snippet: string;
  score: number;
}

// Contact types
export interface Contact {
  email: string;
  displayName: string;
  source: "aerion" | "google" | "vcard" | "sent-history";
  avatarUrl?: string;
  sendCount?: number;
  lastUsed?: Date;
}

// Sync types
export interface SyncStatus {
  accountId: string;
  status: "idle" | "syncing" | "error";
  lastSync?: Date;
  error?: string;
}

// Power state types
export type PowerState = "ac" | "battery" | "low-battery";

export interface AppState {
  powerState: PowerState;
  isUserActive: boolean;
  isAppForeground: boolean;
}
