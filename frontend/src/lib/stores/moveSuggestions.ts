import type { folder } from '../../../wailsjs/go/models'

const STORAGE_KEY = 'aerion.moveSuggestions.v1'
const MAX_BUCKET_ENTRIES = 80

type SuggestionBucket = Record<string, StoredTarget>

interface StoredTarget {
  accountId: string
  folderId: string
  folderName: string
  folderPath?: string
  count: number
  lastUsed: number
}

interface SuggestionStore {
  bySender: Record<string, SuggestionBucket>
  byDomain: Record<string, SuggestionBucket>
}

export interface MoveSuggestion {
  accountId: string
  folderId: string
  folderName: string
  folderPath?: string
  exactCount: number
  domainCount: number
  lastUsed: number
}

function emptyStore(): SuggestionStore {
  return { bySender: {}, byDomain: {} }
}

function normalizeEmail(email: string | null | undefined): string {
  return (email || '').trim().toLowerCase()
}

function getDomain(email: string): string {
  const at = email.lastIndexOf('@')
  return at > -1 ? email.slice(at + 1) : ''
}

function targetKey(accountId: string, folderId: string): string {
  return `${accountId}:${folderId}`
}

function readStore(): SuggestionStore {
  if (typeof localStorage === 'undefined') return emptyStore()
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return emptyStore()
    const parsed = JSON.parse(raw) as Partial<SuggestionStore>
    return {
      bySender: parsed.bySender || {},
      byDomain: parsed.byDomain || {},
    }
  } catch {
    return emptyStore()
  }
}

function writeStore(store: SuggestionStore) {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(STORAGE_KEY, JSON.stringify(store))
}

function trimBucket(bucket: SuggestionBucket) {
  const entries = Object.entries(bucket)
  if (entries.length <= MAX_BUCKET_ENTRIES) return
  entries
    .sort(([, a], [, b]) => b.lastUsed - a.lastUsed)
    .slice(MAX_BUCKET_ENTRIES)
    .forEach(([key]) => delete bucket[key])
}

function incrementBucket(
  buckets: Record<string, SuggestionBucket>,
  bucketKey: string,
  target: Omit<StoredTarget, 'count' | 'lastUsed'>,
) {
  if (!bucketKey) return
  const bucket = buckets[bucketKey] || {}
  const key = targetKey(target.accountId, target.folderId)
  const existing = bucket[key]
  bucket[key] = {
    ...target,
    count: (existing?.count || 0) + 1,
    lastUsed: Date.now(),
  }
  trimBucket(bucket)
  buckets[bucketKey] = bucket
}

export function learnMoveSuggestion(
  senderEmail: string | null | undefined,
  target: { accountId: string; folderId: string; folderName: string; folderPath?: string },
) {
  const email = normalizeEmail(senderEmail)
  if (!email || !target.accountId || !target.folderId) return

  const store = readStore()
  const storedTarget = {
    accountId: target.accountId,
    folderId: target.folderId,
    folderName: target.folderName,
    folderPath: target.folderPath,
  }

  incrementBucket(store.bySender, email, storedTarget)
  incrementBucket(store.byDomain, getDomain(email), storedTarget)
  writeStore(store)
}

export function getMoveSuggestions(
  senderEmail: string | null | undefined,
  availableFolders: folder.Folder[],
  selectedAccountId: string,
  limit = 4,
): MoveSuggestion[] {
  const email = normalizeEmail(senderEmail)
  if (!email || availableFolders.length === 0) return []

  const store = readStore()
  const domain = getDomain(email)
  const senderBucket = store.bySender[email] || {}
  const domainBucket = store.byDomain[domain] || {}
  const available = new Map(
    availableFolders.map((f) => [targetKey(selectedAccountId, f.id), f])
  )
  const keys = new Set([...Object.keys(senderBucket), ...Object.keys(domainBucket)])

  return [...keys]
    .filter((key) => available.has(key))
    .map((key) => {
      const folderInfo = available.get(key)!
      const exact = senderBucket[key]
      const domainMatch = domainBucket[key]
      return {
        accountId: selectedAccountId,
        folderId: folderInfo.id,
        folderName: folderInfo.name,
        folderPath: folderInfo.path,
        exactCount: exact?.count || 0,
        domainCount: domainMatch?.count || 0,
        lastUsed: Math.max(exact?.lastUsed || 0, domainMatch?.lastUsed || 0),
      }
    })
    .sort((a, b) => {
      const aSpecificity = a.exactCount > 0 ? 1 : 0
      const bSpecificity = b.exactCount > 0 ? 1 : 0
      if (aSpecificity !== bSpecificity) return bSpecificity - aSpecificity
      if (a.exactCount !== b.exactCount) return b.exactCount - a.exactCount
      if (a.domainCount !== b.domainCount) return b.domainCount - a.domainCount
      return b.lastUsed - a.lastUsed
    })
    .slice(0, limit)
}
