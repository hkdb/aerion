/**
 * Composer API Abstraction Layer
 *
 * Provides a unified interface for composer operations that works in both:
 * - Main window (modal/inline composer) - uses App bindings
 * - Detached composer window - uses ComposerApp bindings
 *
 * The API is injected via Svelte context to allow different implementations
 * depending on the window type.
 */
// @ts-ignore - Wails generated imports
import {
  account,
  app,
  contact,
  draft,
  pgp,
  smime,
  smtp
} from "../../wailsjs/go/models";

/**
 * Interface for composer API operations.
 * Both App and ComposerApp implement these methods with the same signatures.
 */
export interface ComposerApi {
  /** Send a composed email */
  sendMessage: (
    accountId: string,
    message: smtp.ComposeMessage
  ) => Promise<void>;

  /** Search contacts for autocomplete */
  searchContacts: (query: string, limit: number) => Promise<contact.Contact[]>;

  /** Get identities for an account */
  getIdentities: (accountId: string) => Promise<account.Identity[]>;

  /** Save a draft (creates new or updates existing if draftId provided) */
  saveDraft: (
    accountId: string,
    message: smtp.ComposeMessage,
    draftId: string
  ) => Promise<{ id: string; syncStatus: string }>;

  /** Delete a draft */
  deleteDraft: (draftId: string) => Promise<void>;

  /** Pick attachment files via native file picker */
  pickAttachmentFiles: () => Promise<app.ComposerAttachment[]>;

  /** Get account details */
  getAccount: (accountId: string) => Promise<account.Account>;

  /** Check if account has a valid default S/MIME certificate */
  hasSMIMECertificate: (accountId: string) => Promise<boolean>;

  /** Get the S/MIME certificate matching a specific email (for identity-aware bar visibility) */
  getSMIMECertificateForEmail: (
    accountId: string,
    email: string
  ) => Promise<smime.Certificate | null>;

  /** Get the S/MIME signing policy for an account */
  getSMIMESignPolicy: (accountId: string) => Promise<string>;

  /** Get the S/MIME encryption policy for an account */
  getSMIMEEncryptPolicy: (accountId: string) => Promise<string>;

  /** Check which recipients have S/MIME certificates available */
  checkRecipientCerts: (emails: string[]) => Promise<Record<string, boolean>>;

  /** Open a file picker for recipient certificate files */
  pickRecipientCertFile: () => Promise<string>;

  /** Import a recipient's public certificate from a file */
  importRecipientCert: (email: string, filePath: string) => Promise<void>;

  /** Check if account has a valid default PGP key */
  hasPGPKey: (accountId: string) => Promise<boolean>;

  /** Get the PGP key matching a specific email (for identity-aware bar visibility) */
  getPGPKeyForEmail: (
    accountId: string,
    email: string
  ) => Promise<pgp.Key | null>;

  /** Get the PGP signing policy for an account */
  getPGPSignPolicy: (accountId: string) => Promise<string>;

  /** Get the PGP encryption policy for an account */
  getPGPEncryptPolicy: (accountId: string) => Promise<string>;

  /** Check which recipients have PGP public keys available */
  checkRecipientPGPKeys: (emails: string[]) => Promise<Record<string, boolean>>;

  /** Open a file picker for recipient PGP public key files */
  pickRecipientPGPKeyFile: () => Promise<string>;

  /** Import a recipient's PGP public key from a file */
  importRecipientPGPKey: (email: string, filePath: string) => Promise<void>;

  /** Perform a WKD lookup for a recipient's PGP key */
  lookupWKD: (email: string) => Promise<string>;

  /** Perform an HKP key server lookup for a recipient's PGP key */
  lookupHKP: (email: string) => Promise<string>;

  /** Perform a unified WKD+HKP lookup for a recipient's PGP key */
  lookupPGPKey: (email: string) => Promise<string>;

  /** Read a file from a filesystem path as an attachment */
  readFileAsAttachment: (
    filePath: string
  ) => Promise<app.ComposerAttachment | null>;

  /** Check if running inside a Flatpak sandbox */
  isFlatpak: () => Promise<boolean>;

  /**
   * Get all accounts with their identities (only available in main window).
   * Returns undefined in detached composer windows.
   */
  getAllAccountIdentities?: () => Promise<app.AccountIdentityGroup[]>;

  /**
   * Open a detached composer window (only available in main window).
   * Returns undefined in detached composer windows.
   */
  openComposerWindow?: (
    accountId: string,
    mode: string,
    messageId: string,
    draftId: string,
    mailtoURL?: string
  ) => Promise<void>;
}

/**
 * Context key for accessing the composer API.
 * Use with getContext/setContext.
 */
export const COMPOSER_API_KEY = "composer-api";

/**
 * Creates the composer API implementation for the main window.
 * Uses App bindings.
 */
export function createMainWindowApi(): ComposerApi {
  // Dynamic import to avoid bundling issues
  // These will be resolved at runtime based on which entry point is used
  return {
    sendMessage: async (accountId: string, message: smtp.ComposeMessage) => {
      const { SendMessage } = await import("../../wailsjs/go/app/App.js");
      return SendMessage(accountId, message);
    },

    searchContacts: async (query: string, limit: number) => {
      const { SearchContacts } = await import("../../wailsjs/go/app/App.js");
      return SearchContacts(query, limit) || [];
    },

    getIdentities: async (accountId: string) => {
      const { GetIdentities } = await import("../../wailsjs/go/app/App.js");
      return GetIdentities(accountId);
    },

    saveDraft: async (
      accountId: string,
      message: smtp.ComposeMessage,
      draftId: string
    ) => {
      const { SaveDraft } = await import("../../wailsjs/go/app/App.js");
      const result = await SaveDraft(accountId, message, draftId);
      return {
        id: result?.draft?.id || "",
        syncStatus: result?.draft?.syncStatus || "pending"
      };
    },

    deleteDraft: async (draftId: string) => {
      const { DeleteDraft } = await import("../../wailsjs/go/app/App.js");
      return DeleteDraft(draftId);
    },

    pickAttachmentFiles: async () => {
      const { PickAttachmentFiles } =
        await import("../../wailsjs/go/app/App.js");
      return PickAttachmentFiles();
    },

    getAccount: async (accountId: string) => {
      const { GetAccount } = await import("../../wailsjs/go/app/App.js");
      return GetAccount(accountId);
    },

    hasSMIMECertificate: async (accountId: string) => {
      const { HasSMIMECertificate } =
        await import("../../wailsjs/go/app/App.js");
      return HasSMIMECertificate(accountId);
    },

    getSMIMECertificateForEmail: async (accountId: string, email: string) => {
      const { GetSMIMECertificateForEmail } =
        await import("../../wailsjs/go/app/App.js");
      return GetSMIMECertificateForEmail(accountId, email);
    },

    getSMIMESignPolicy: async (accountId: string) => {
      const { GetSMIMESignPolicy } =
        await import("../../wailsjs/go/app/App.js");
      return GetSMIMESignPolicy(accountId);
    },

    getSMIMEEncryptPolicy: async (accountId: string) => {
      const { GetSMIMEEncryptPolicy } =
        await import("../../wailsjs/go/app/App.js");
      return GetSMIMEEncryptPolicy(accountId);
    },

    checkRecipientCerts: async (emails: string[]) => {
      const { CheckRecipientCerts } =
        await import("../../wailsjs/go/app/App.js");
      return CheckRecipientCerts(emails);
    },

    pickRecipientCertFile: async () => {
      const { PickRecipientCertFile } =
        await import("../../wailsjs/go/app/App.js");
      return PickRecipientCertFile();
    },

    importRecipientCert: async (email: string, filePath: string) => {
      const { ImportRecipientCert } =
        await import("../../wailsjs/go/app/App.js");
      return ImportRecipientCert(email, filePath);
    },

    hasPGPKey: async (accountId: string) => {
      const { HasPGPKey } = await import("../../wailsjs/go/app/App.js");
      return HasPGPKey(accountId);
    },

    getPGPKeyForEmail: async (accountId: string, email: string) => {
      const { GetPGPKeyForEmail } = await import("../../wailsjs/go/app/App.js");
      return GetPGPKeyForEmail(accountId, email);
    },

    getPGPSignPolicy: async (accountId: string) => {
      const { GetPGPSignPolicy } = await import("../../wailsjs/go/app/App.js");
      return GetPGPSignPolicy(accountId);
    },

    getPGPEncryptPolicy: async (accountId: string) => {
      const { GetPGPEncryptPolicy } =
        await import("../../wailsjs/go/app/App.js");
      return GetPGPEncryptPolicy(accountId);
    },

    checkRecipientPGPKeys: async (emails: string[]) => {
      const { CheckRecipientPGPKeys } =
        await import("../../wailsjs/go/app/App.js");
      return CheckRecipientPGPKeys(emails);
    },

    pickRecipientPGPKeyFile: async () => {
      const { PickRecipientPGPKeyFile } =
        await import("../../wailsjs/go/app/App.js");
      return PickRecipientPGPKeyFile();
    },

    importRecipientPGPKey: async (email: string, filePath: string) => {
      const { ImportRecipientPGPKey } =
        await import("../../wailsjs/go/app/App.js");
      return ImportRecipientPGPKey(email, filePath);
    },

    lookupWKD: async (email: string) => {
      const { LookupWKD } = await import("../../wailsjs/go/app/App.js");
      return LookupWKD(email);
    },

    lookupHKP: async (email: string) => {
      const { LookupHKP } = await import("../../wailsjs/go/app/App.js");
      return LookupHKP(email);
    },

    lookupPGPKey: async (email: string) => {
      const { LookupPGPKey } = await import("../../wailsjs/go/app/App.js");
      return LookupPGPKey(email);
    },

    readFileAsAttachment: async (filePath: string) => {
      const { ReadFileAsAttachment } =
        await import("../../wailsjs/go/app/App.js");
      return ReadFileAsAttachment(filePath);
    },

    isFlatpak: async () => {
      const { IsFlatpak } = await import("../../wailsjs/go/app/App.js");
      return IsFlatpak();
    },

    getAllAccountIdentities: async () => {
      const { GetAllAccountIdentities } =
        await import("../../wailsjs/go/app/App.js");
      return GetAllAccountIdentities();
    },

    openComposerWindow: async (
      accountId: string,
      mode: string,
      messageId: string,
      draftId: string,
      mailtoURL?: string
    ) => {
      const { OpenComposerWindow } =
        await import("../../wailsjs/go/app/App.js");
      return OpenComposerWindow(
        accountId,
        mode,
        messageId,
        draftId,
        mailtoURL || ""
      );
    }
  };
}

/**
 * Creates the composer API implementation for the detached composer window.
 * Uses ComposerApp bindings.
 */
export function createComposerWindowApi(accountId: string): ComposerApi {
  return {
    sendMessage: async (accountId: string, message: smtp.ComposeMessage) => {
      const { SendMessage } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return SendMessage(accountId, message);
    },

    searchContacts: async (query: string, limit: number) => {
      const { SearchContacts } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return SearchContacts(query, limit) || [];
    },

    getIdentities: async (accountId: string) => {
      const { GetIdentities } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return GetIdentities(accountId);
    },

    saveDraft: async (
      accountId: string,
      message: smtp.ComposeMessage,
      draftId: string
    ) => {
      const { SaveDraft } = await import("../../wailsjs/go/app/ComposerApp.js");
      const result = await SaveDraft(accountId, message, draftId || "");
      return {
        id: result?.id || "",
        syncStatus: result?.syncStatus || "pending"
      };
    },

    deleteDraft: async (draftId: string) => {
      const { DeleteDraft } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return DeleteDraft(draftId);
    },

    pickAttachmentFiles: async () => {
      const { PickAttachmentFiles } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return PickAttachmentFiles();
    },

    getAccount: async (accountId: string) => {
      const { GetAccount } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return GetAccount(accountId);
    },

    hasSMIMECertificate: async (accountId: string) => {
      const { HasSMIMECertificate } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return HasSMIMECertificate(accountId);
    },

    getSMIMECertificateForEmail: async (accountId: string, email: string) => {
      const { GetSMIMECertificateForEmail } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return GetSMIMECertificateForEmail(accountId, email);
    },

    getSMIMESignPolicy: async (accountId: string) => {
      const { GetSMIMESignPolicy } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return GetSMIMESignPolicy(accountId);
    },

    getSMIMEEncryptPolicy: async (accountId: string) => {
      const { GetSMIMEEncryptPolicy } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return GetSMIMEEncryptPolicy(accountId);
    },

    checkRecipientCerts: async (emails: string[]) => {
      const { CheckRecipientCerts } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return CheckRecipientCerts(emails);
    },

    pickRecipientCertFile: async () => {
      const { PickRecipientCertFile } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return PickRecipientCertFile();
    },

    importRecipientCert: async (email: string, filePath: string) => {
      const { ImportRecipientCert } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return ImportRecipientCert(email, filePath);
    },

    hasPGPKey: async (accountId: string) => {
      const { HasPGPKey } = await import("../../wailsjs/go/app/ComposerApp.js");
      return HasPGPKey(accountId);
    },

    getPGPKeyForEmail: async (accountId: string, email: string) => {
      const { GetPGPKeyForEmail } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return GetPGPKeyForEmail(accountId, email);
    },

    getPGPSignPolicy: async (accountId: string) => {
      const { GetPGPSignPolicy } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return GetPGPSignPolicy(accountId);
    },

    getPGPEncryptPolicy: async (accountId: string) => {
      const { GetPGPEncryptPolicy } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return GetPGPEncryptPolicy(accountId);
    },

    checkRecipientPGPKeys: async (emails: string[]) => {
      const { CheckRecipientPGPKeys } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return CheckRecipientPGPKeys(emails);
    },

    pickRecipientPGPKeyFile: async () => {
      const { PickRecipientPGPKeyFile } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return PickRecipientPGPKeyFile();
    },

    importRecipientPGPKey: async (email: string, filePath: string) => {
      const { ImportRecipientPGPKey } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return ImportRecipientPGPKey(email, filePath);
    },

    lookupWKD: async (email: string) => {
      const { LookupWKD } = await import("../../wailsjs/go/app/ComposerApp.js");
      return LookupWKD(email);
    },

    lookupHKP: async (email: string) => {
      const { LookupHKP } = await import("../../wailsjs/go/app/ComposerApp.js");
      return LookupHKP(email);
    },

    lookupPGPKey: async (email: string) => {
      const { LookupPGPKey } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return LookupPGPKey(email);
    },

    readFileAsAttachment: async (filePath: string) => {
      const { ReadFileAsAttachment } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return ReadFileAsAttachment(filePath);
    },

    isFlatpak: async () => {
      const { IsFlatpak } = await import("../../wailsjs/go/app/ComposerApp.js");
      return IsFlatpak();
    },

    getAllAccountIdentities: async () => {
      const { GetAllAccountIdentities } =
        await import("../../wailsjs/go/app/ComposerApp.js");
      return GetAllAccountIdentities();
    }
  };
}
