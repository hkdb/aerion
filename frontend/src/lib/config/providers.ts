import yahooIcon from '$lib/icons/providers/yahoo.svg?url'
import icloudIcon from '$lib/icons/providers/icloud.svg?url'
import protonmailIcon from '$lib/icons/providers/protonmail.svg?url'
import fastmailIcon from '$lib/icons/providers/fastmail.svg?url'
import zohoIcon from '$lib/icons/providers/zoho.svg?url'
import gmxIcon from '$lib/icons/providers/gmx.svg?url'
import mailcomIcon from '$lib/icons/providers/mailcom.svg?url'

export type SecurityType = 'none' | 'tls' | 'starttls'
export type AuthMethod = 'password' | 'oauth2'
export type OAuthProvider = 'google' | 'microsoft'

export interface ServerConfig {
  host: string
  port: number
  security: SecurityType
}

export interface OAuthConfig {
  provider: OAuthProvider
  // OAuth can also fall back to password (app password) for Gmail
  allowPasswordFallback?: boolean
}

export interface EmailProvider {
  id: string
  name: string
  icon: string // iconify icon name (fallback)
  iconSrc?: string // local SVG URL (takes precedence over icon)
  domains: string[] // for auto-detection from email
  imap: ServerConfig
  smtp: ServerConfig
  notes?: string // e.g., "Requires App Password"
  notesKey?: string
  usernameIsEmail?: boolean // defaults to true
  // OAuth configuration
  authMethod?: AuthMethod // defaults to 'password'
  oauth?: OAuthConfig
}

export const providers: EmailProvider[] = [
  {
    id: 'gmail',
    name: 'Gmail',
    icon: 'logos:google-gmail',
    domains: ['gmail.com', 'googlemail.com'],
    imap: { host: 'imap.gmail.com', port: 993, security: 'tls' },
    smtp: { host: 'smtp.gmail.com', port: 587, security: 'starttls' },
    notes: 'Sign in with Google or use App Password',
    notesKey: 'account.notesGoogle',
    usernameIsEmail: true,
    authMethod: 'oauth2',
    oauth: { provider: 'google', allowPasswordFallback: true },
  },
  {
    id: 'outlook',
    name: 'Outlook / Hotmail',
    icon: 'logos:microsoft-icon',
    domains: ['outlook.com', 'hotmail.com', 'live.com', 'msn.com'],
    imap: { host: 'outlook.office365.com', port: 993, security: 'tls' },
    smtp: { host: 'smtp.office365.com', port: 587, security: 'starttls' },
    usernameIsEmail: true,
    authMethod: 'oauth2',
    oauth: { provider: 'microsoft', allowPasswordFallback: false },
  },
  {
    id: 'yahoo',
    name: 'Yahoo Mail',
    icon: 'logos:yahoo',
    iconSrc: yahooIcon,
    domains: ['yahoo.com', 'ymail.com', 'yahoo.co.uk', 'yahoo.ca'],
    imap: { host: 'imap.mail.yahoo.com', port: 993, security: 'tls' },
    smtp: { host: 'smtp.mail.yahoo.com', port: 587, security: 'starttls' },
    notes: 'Requires App Password (enable 2-Step Verification first)',
    notesKey: 'account.notesYahoo',
    usernameIsEmail: true,
  },
  {
    id: 'icloud',
    name: 'iCloud Mail',
    icon: 'simple-icons:icloud',
    iconSrc: icloudIcon,
    domains: ['icloud.com', 'me.com', 'mac.com'],
    imap: { host: 'imap.mail.me.com', port: 993, security: 'tls' },
    smtp: { host: 'smtp.mail.me.com', port: 587, security: 'starttls' },
    notes: 'Requires App-Specific Password from appleid.apple.com',
    notesKey: 'account.notesIcloud',
    usernameIsEmail: true,
  },
  {
    id: 'protonmail',
    name: 'ProtonMail Bridge',
    icon: 'simple-icons:protonmail',
    iconSrc: protonmailIcon,
    domains: ['protonmail.com', 'proton.me', 'pm.me'],
    imap: { host: '127.0.0.1', port: 1143, security: 'starttls' },
    smtp: { host: '127.0.0.1', port: 1025, security: 'starttls' },
    notes: 'Requires ProtonMail Bridge running locally',
    notesKey: 'account.notesProton',
    usernameIsEmail: true,
  },
  {
    id: 'fastmail',
    name: 'Fastmail',
    icon: 'simple-icons:fastmail',
    iconSrc: fastmailIcon,
    domains: ['fastmail.com', 'fastmail.fm', 'messagingengine.com'],
    imap: { host: 'imap.fastmail.com', port: 993, security: 'tls' },
    smtp: { host: 'smtp.fastmail.com', port: 587, security: 'starttls' },
    notes: 'Use App Password from Settings > Privacy & Security',
    notesKey: 'account.notesFastmail',
    usernameIsEmail: true,
  },
  {
    id: 'zoho',
    name: 'Zoho Mail',
    icon: 'simple-icons:zoho',
    iconSrc: zohoIcon,
    domains: ['zoho.com', 'zohomail.com'],
    imap: { host: 'imap.zoho.com', port: 993, security: 'tls' },
    smtp: { host: 'smtp.zoho.com', port: 587, security: 'starttls' },
    usernameIsEmail: true,
  },
  {
    id: 'aol',
    name: 'AOL Mail',
    icon: 'simple-icons:aol',
    domains: ['aol.com', 'aim.com'],
    imap: { host: 'imap.aol.com', port: 993, security: 'tls' },
    smtp: { host: 'smtp.aol.com', port: 587, security: 'starttls' },
    notes: 'Requires App Password',
    notesKey: 'account.notesAppPassword',
    usernameIsEmail: true,
  },
  {
    id: 'gmx',
    name: 'GMX Mail',
    icon: 'mdi:email-outline',
    iconSrc: gmxIcon,
    domains: ['gmx.com', 'gmx.net', 'gmx.de'],
    imap: { host: 'imap.gmx.com', port: 993, security: 'tls' },
    smtp: { host: 'mail.gmx.com', port: 587, security: 'starttls' },
    usernameIsEmail: true,
  },
  {
    id: 'mailcom',
    name: 'Mail.com',
    icon: 'mdi:email-outline',
    iconSrc: mailcomIcon,
    domains: ['mail.com'],
    imap: { host: 'imap.mail.com', port: 993, security: 'tls' },
    smtp: { host: 'smtp.mail.com', port: 587, security: 'starttls' },
    usernameIsEmail: true,
  },
  // Custom/Manual option - always last
  {
    id: 'custom',
    name: 'Other / Manual',
    icon: 'mdi:cog-outline',
    domains: [],
    imap: { host: '', port: 993, security: 'tls' },
    smtp: { host: '', port: 587, security: 'starttls' },
    usernameIsEmail: true,
  },
]

/**
 * Detect email provider from email address domain
 */
export function detectProvider(email: string): EmailProvider | null {
  const domain = email.split('@')[1]?.toLowerCase()
  if (!domain) return null
  
  // Find matching provider (excluding 'custom')
  const provider = providers.find(
    (p) => p.id !== 'custom' && p.domains.includes(domain)
  )
  
  return provider ?? null
}

/**
 * Get provider by ID
 */
export function getProvider(id: string): EmailProvider | undefined {
  return providers.find((p) => p.id === id)
}

/**
 * Get the custom/manual provider
 */
export function getCustomProvider(): EmailProvider {
  return providers.find((p) => p.id === 'custom')!
}

/**
 * Check if a provider supports OAuth
 */
export function isOAuthProvider(provider: EmailProvider): boolean {
  return provider.authMethod === 'oauth2' && !!provider.oauth
}

/**
 * Check if a provider allows password fallback (for OAuth providers)
 */
export function allowsPasswordFallback(provider: EmailProvider): boolean {
  return provider.oauth?.allowPasswordFallback ?? false
}

/**
 * Get the OAuth provider type for an email provider
 */
export function getOAuthProviderType(provider: EmailProvider): OAuthProvider | null {
  return provider.oauth?.provider ?? null
}

/**
 * Get all OAuth-enabled providers
 */
export function getOAuthProviders(): EmailProvider[] {
  return providers.filter(isOAuthProvider)
}

/**
 * Security type options for select dropdowns
 */
export const securityOptions = [
  { value: 'tls', label: 'SSL/TLS', labelKey: 'account.securitySsl' },
  { value: 'starttls', label: 'STARTTLS', labelKey: 'account.securityStarttls' },
  { value: 'none', label: 'None (insecure)', labelKey: 'account.securityNone' },
] as const

/**
 * Common sync period options (in days)
 */
export const syncPeriodOptions = [
  { value: 7,   label: '1 week',       labelKey: 'account.sync1week' },
  { value: 14,  label: '2 weeks',      labelKey: 'account.sync2weeks' },
  { value: 30,  label: '1 month',      labelKey: 'account.sync1month' },
  { value: 60,  label: '2 months',     labelKey: 'account.sync2months' },
  { value: 90,  label: '3 months',     labelKey: 'account.sync3months' },
  { value: 180, label: '6 months',     labelKey: 'account.sync6months' },
  { value: 365, label: '1 year',       labelKey: 'account.sync1year' },
  { value: 0,   label: 'All messages', labelKey: 'account.syncAllMessages' },
] as const

/**
 * Sync interval options (in minutes) for automatic email checking
 */
export const syncIntervalOptions = [
  { value: 0,  label: 'Manual only',       labelKey: 'account.manualOnly' },
  { value: 5,  label: 'Every 5 minutes',   labelKey: 'account.every5Min' },
  { value: 15, label: 'Every 15 minutes',  labelKey: 'account.every15Min' },
  { value: 30, label: 'Every 30 minutes',  labelKey: 'account.every30Min' },
  { value: 60, label: 'Every hour',        labelKey: 'account.everyHour' },
] as const
