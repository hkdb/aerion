/**
 * OAuth state store for managing OAuth2 authentication flows
 */

import {
  StartOAuthFlow,
  CancelOAuthFlow,
  GetOAuthStatus,
  IsOAuthConfigured,
  GetConfiguredOAuthProviders,
  CompleteOAuthAccountSetup,
  SaveOAuthTokens,
  SavePendingOAuthTokens,
  ReauthorizeAccount,
  TestOAuthConnection,
  GetAccount,
} from '../../../wailsjs/go/app/App'
// @ts-ignore - wailsjs runtime
import { EventsOn, EventsOff } from '../../../wailsjs/runtime/runtime'
import { get } from 'svelte/store'
import { _ } from '$lib/i18n'
import { addToast } from './toast'

export type OAuthFlowState = 'idle' | 'pending' | 'success' | 'error' | 'cancelled'
export type OAuthProvider = 'google' | 'microsoft'

export interface OAuthFlowResult {
  provider: OAuthProvider
  email: string
  expiresIn: number
}

export interface OAuthStatus {
  isOAuth: boolean
  provider: string
  email: string
  expiresAt: string
  isExpired: boolean
  needsReauth: boolean
}

class OAuthStore {
  // Flow state
  flowState = $state<OAuthFlowState>('idle')
  flowProvider = $state<OAuthProvider | null>(null)
  flowError = $state<string | null>(null)
  flowResult = $state<OAuthFlowResult | null>(null)
  // Authorization URL — exposed so the UI can offer a "Copy link" fallback
  // when the browser fails to open (e.g., portal misconfig in Flatpak).
  authURL = $state<string | null>(null)

  // Configured providers (cached)
  private configuredProviders = $state<OAuthProvider[]>([])
  private configuredLoaded = false

  // Event listener cleanup tracking
  private eventsInitialized = false

  /**
   * Initialize event listeners for OAuth events from backend.
   * Should be called once when the app starts.
   */
  initEvents(): void {
    if (this.eventsInitialized) return
    this.eventsInitialized = true

    EventsOn('oauth:started', (data: { provider: string; authURL?: string }) => {
      this.flowState = 'pending'
      this.flowProvider = data.provider as OAuthProvider
      this.flowError = null
      this.flowResult = null
      this.authURL = data.authURL ?? null
    })

    EventsOn('oauth:success', (data: { provider: string; email: string; expiresIn: number }) => {
      this.flowState = 'success'
      this.flowResult = {
        provider: data.provider as OAuthProvider,
        email: data.email,
        expiresIn: data.expiresIn,
      }
      this.flowError = null
    })

    EventsOn('oauth:error', (data: { provider: string; error: string }) => {
      this.flowState = 'error'
      this.flowError = data.error
      this.flowResult = null
    })

    EventsOn('oauth:cancelled', () => {
      this.flowState = 'cancelled'
      this.flowError = null
      this.flowResult = null
    })

    // Listen for reauth required events (token refresh failed)
    EventsOn('oauth:reauth-required', async (data: { accountId: string; provider: string; error: string }) => {
      // Get account name for better UX
      let accountName = data.provider
      try {
        const account = await GetAccount(data.accountId)
        if (account?.name) {
          accountName = account.name
        }
      } catch {
        // Ignore error, use provider name as fallback
      }

      // Show toast notification to user
      addToast({
        type: 'error',
        message: get(_)('toast.oauthExpired', { values: { name: accountName } }),
        duration: 10000, // Show for 10 seconds
      })
    })
  }

  /**
   * Cleanup event listeners.
   * Call this when the app is shutting down.
   */
  cleanupEvents(): void {
    if (!this.eventsInitialized) return
    this.eventsInitialized = false

    EventsOff('oauth:started')
    EventsOff('oauth:success')
    EventsOff('oauth:error')
    EventsOff('oauth:cancelled')
    EventsOff('oauth:reauth-required')
  }

  /**
   * Start OAuth flow for a provider.
   * Opens the browser for authorization.
   */
  async startFlow(provider: OAuthProvider): Promise<void> {
    try {
      this.flowState = 'pending'
      this.flowProvider = provider
      this.flowError = null
      this.flowResult = null
      this.authURL = null

      await StartOAuthFlow(provider)
      // State will be updated via events
    } catch (err) {
      this.flowState = 'error'
      this.flowError = err instanceof Error ? err.message : String(err)
      throw err
    }
  }

  /**
   * Cancel any in-progress OAuth flow.
   */
  cancelFlow(): void {
    CancelOAuthFlow()
    this.reset()
  }

  /**
   * Reset the OAuth flow state.
   */
  reset(): void {
    this.flowState = 'idle'
    this.flowProvider = null
    this.flowError = null
    this.flowResult = null
    this.authURL = null
  }

  /**
   * Complete account setup after successful OAuth flow.
   * Creates the account and stores the tokens.
   */
  async completeAccountSetup(
    accountName: string,
    displayName: string,
    color: string,
    accessToken: string,
    refreshToken: string
  ): Promise<{ accountId: string; email: string }> {
    if (this.flowState !== 'success' || !this.flowResult) {
      throw new Error('No successful OAuth flow to complete')
    }

    const { provider, email, expiresIn } = this.flowResult

    // Create the account
    const account = await CompleteOAuthAccountSetup(provider, email, accountName, displayName, color)

    // Save the OAuth tokens
    await SaveOAuthTokens(account.id, provider, accessToken, refreshToken, expiresIn)

    return { accountId: account.id, email }
  }

  /**
   * Check if a provider is configured (has client ID).
   */
  async isProviderConfigured(provider: OAuthProvider): Promise<boolean> {
    return await IsOAuthConfigured(provider)
  }

  /**
   * Get list of configured OAuth providers.
   * Results are cached after first call.
   */
  async getConfiguredProviders(): Promise<OAuthProvider[]> {
    if (!this.configuredLoaded) {
      const providers = await GetConfiguredOAuthProviders()
      this.configuredProviders = providers as OAuthProvider[]
      this.configuredLoaded = true
    }
    return this.configuredProviders
  }

  /**
   * Check if OAuth is available (at least one provider configured).
   */
  async isOAuthAvailable(): Promise<boolean> {
    const providers = await this.getConfiguredProviders()
    return providers.length > 0
  }

  /**
   * Get OAuth status for an account.
   */
  async getAccountStatus(accountId: string): Promise<OAuthStatus> {
    return await GetOAuthStatus(accountId)
  }

  /**
   * Re-authorize an account (when tokens have expired).
   * Starts OAuth flow and waits for completion, then saves new tokens.
   */
  async reauthorize(accountId: string): Promise<void> {
    // Ensure event listeners are initialized
    this.initEvents()

    // Reset state before starting
    this.reset()

    // Start the OAuth flow
    await ReauthorizeAccount(accountId)

    // Wait for the OAuth flow to complete
    return new Promise((resolve, reject) => {
      const checkInterval = setInterval(() => {
        if (this.flowState === 'success' && this.flowResult) {
          clearInterval(checkInterval)
          // Save the pending tokens to the existing account
          SavePendingOAuthTokens(accountId)
            .then(() => {
              this.reset()
              resolve()
            })
            .catch((err) => {
              this.reset()
              reject(err)
            })
        } else if (this.flowState === 'error') {
          clearInterval(checkInterval)
          const error = this.flowError || 'OAuth flow failed'
          this.reset()
          reject(new Error(error))
        } else if (this.flowState === 'cancelled') {
          clearInterval(checkInterval)
          this.reset()
          reject(new Error('OAuth flow was cancelled'))
        }
      }, 100)

      // Timeout after 5 minutes
      setTimeout(() => {
        clearInterval(checkInterval)
        if (this.flowState === 'pending') {
          this.reset()
          reject(new Error('OAuth flow timed out'))
        }
      }, 5 * 60 * 1000)
    })
  }

  /**
   * Test OAuth connection for an account.
   */
  async testConnection(accountId: string): Promise<void> {
    await TestOAuthConnection(accountId)
  }

  /**
   * Check if the current flow is for a specific provider.
   */
  isFlowForProvider(provider: OAuthProvider): boolean {
    return this.flowProvider === provider
  }

  /**
   * Check if OAuth flow is in progress.
   */
  get isFlowPending(): boolean {
    return this.flowState === 'pending'
  }

  /**
   * Check if OAuth flow completed successfully.
   */
  get isFlowSuccess(): boolean {
    return this.flowState === 'success'
  }

  /**
   * Check if OAuth flow failed.
   */
  get isFlowError(): boolean {
    return this.flowState === 'error'
  }
}

// Export singleton instance
export const oauthStore = new OAuthStore()
