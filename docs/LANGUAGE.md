# Contributing Translations

If you'd like to submit a translation PR:

## Checklist

Use this checklist to ensure your submission is complete:

- [ ] **Claimed language** — filed a [Translation issue](https://github.com/hkdb/aerion/issues/new?template=translation.yml) to avoid duplicate efforts
- [ ] **Locale JSON** — `frontend/src/lib/i18n/locales/<code>.json` created with all ~900+ keys translated
- [ ] **Register locale** — added `register()` call in `frontend/src/lib/i18n/index.ts`
- [ ] **Supported locales** — added entry to `supportedLocales` array in the same file
- [ ] **date-fns locale** — added `case` in `frontend/src/lib/i18n/dateFnsLocale.ts`
- [ ] **AppStream metainfo** — added `xml:lang` entries in `build/flatpak/io.github.hkdb.Aerion.metainfo.xml`
- [ ] **Desktop entry** — added `[locale]` suffixed keys in `build/linux/aerion.desktop`
- [ ] **Checks pass** — `npm run check`, `npm run build`, and `go test ./...` all pass
- [ ] **Live tested** — app launched with `make dev`, language switched, all strings verified
- [ ] **Detached composer** — composer window also displays the correct language

## Claim Your Language

Before starting any translation work, **file a [Translation issue](https://github.com/hkdb/aerion/issues/new?template=translation.yml)** to declare your intent. This prevents duplicate efforts and lets maintainers coordinate contributors.

Check [existing translation issues](https://github.com/hkdb/aerion/issues?q=label%3Atranslate) first — if someone is already working on your language, consider collaborating with them instead.

## Branch Target

- **Fork from and submit PRs to the latest release branch** (e.g., `v0.2.1-dev`), never `main`.
- The `main` branch tracks the current production release and is not the target for new contributions.
- Check the repository for the latest release branch name before starting, then fork from that branch to ensure your PR has the correct base.

## Before Submitting

Thoroughly review and verify your work:

1. **Review all translated strings** — check for accuracy, natural phrasing, correct placeholder positioning, and consistent terminology throughout
2. **Run the checks**:
   ```bash
   cd frontend
   npm run check    # TypeScript/Svelte type checking
   npm run build    # Production build
   ```
3. **Run backend tests** (to verify metainfo.xml and desktop file validity):
   ```bash
   go test ./...
   ```
4. **Test in the running app** — launch with `make dev`, switch to your language in Settings > General > Language, and verify:
   - All UI strings display correctly
   - Dynamic strings with `{placeholders}` interpolate properly
   - Date/time formatting works
   - The detached composer also uses the correct language
5. **Verify platform files** — confirm your metainfo.xml entries render correctly in GNOME Software or `appstreamcli validate`

## PR Description

Include in your PR:
- The language being added (name + locale code)
- Confirmation that you ran the checks and tested the app
- Any translation decisions worth noting (e.g., terminology choices for technical terms)

---

## Adding a New Language to Aerion

The following sections walk through adding a new language to Aerion's frontend. The i18n system uses `svelte-i18n` with JSON locale files and lazy loading — only the active locale is loaded at runtime.

## Prerequisites

- Node.js and npm installed
- Familiarity with the target language
- Access to the `frontend/src/lib/i18n/` directory

## Steps

### 1. Create the Locale JSON File

Copy the English source file and translate all values:

```bash
cp frontend/src/lib/i18n/locales/en.json frontend/src/lib/i18n/locales/<code>.json
```

Replace `<code>` with the appropriate BCP 47 locale code (e.g., `ja` for Japanese, `ko` for Korean, `fr` for French, `de` for German).

Open the new file and translate every string value. Keep the JSON keys unchanged — only translate the values.

**Example** (`en.json` → `ja.json`):
```json
{
  "common": {
    "save": "Save",         →  "save": "保存",
    "cancel": "Cancel",     →  "cancel": "キャンセル",
    "delete": "Delete"      →  "delete": "削除"
  }
}
```

For a complete, real-world example of a translated locale file, refer to `frontend/src/lib/i18n/locales/zh-HK.json` (Traditional Chinese, Hong Kong).

**Important notes**:
- Preserve `{placeholder}` tokens exactly as-is — these are ICU MessageFormat interpolation variables
  - Example: `"undone": "Undone: {description}"` → `"undone": "取り消し: {description}"`
- **Reposition `{placeholder}` tokens** to match your language's grammar — don't assume the English word order is correct for your language
  - Some placeholders contain localized relative time strings from date-fns (e.g., `{time}` may render as "2 minutes ago" or "2 分鐘前")
  - Example: English `"synced": "Synced {time}"` → Chinese `"synced": "{time}同步"` (time goes before the verb in Chinese)
- Do not translate JSON keys (left side of `:`)
- The file has ~900+ keys organized by namespace: `common`, `sidebar`, `messageList`, `viewer`, `composer`, `contextMenu`, `toast`, `responsive`, `settings`, `settingsAbout`, `settingsAccounts`, `settingsGeneral`, `editor`, `account`, `identity`, `security`, `contactSource`, `certificate`, `terms`, `dialog`, `date`, `aria`, `window`, `attachment`, `search`, `sort`, `oauth`

### 2. Register the Locale

Edit `frontend/src/lib/i18n/index.ts` and add a `register()` call for the new locale:

```typescript
register('en', () => import('./locales/en.json'))
register('cs', () => import('./locales/cs.json'))
register('fr', () => import('./locales/fr.json'))
register('ja', () => import('./locales/ja.json'))     // ← Insert alphabetically (English stays first)
register('zh-CN', () => import('./locales/zh-CN.json'))
register('zh-HK', () => import('./locales/zh-HK.json'))
register('zh-TW', () => import('./locales/zh-TW.json'))
```

### 3. Add to Supported Locales

In the same file (`frontend/src/lib/i18n/index.ts`), add the locale to the `supportedLocales` array:

```typescript
export const supportedLocales = [
  { code: 'en', name: 'English' },
  { code: 'cs', name: 'Čeština' },
  { code: 'fr', name: 'Français' },
  { code: 'ja', name: '日本語' },                     // ← Insert alphabetically (English stays first)
  { code: 'zh-CN', name: '简体中文 (中国)' },
  { code: 'zh-HK', name: '繁體中文 (香港)' },
  { code: 'zh-TW', name: '繁體中文 (台灣)' },
] as const
```

**Ordering convention**: English is always first; the rest are alphabetical by locale code.

Use the language's native name for the `name` field — this is what appears in the Settings language picker.

### 4. Add date-fns Locale (for Date Formatting)

Edit `frontend/src/lib/i18n/dateFnsLocale.ts` and add a case to the switch statement in `loadDateFnsLocale()`:

```typescript
switch (code) {
  case 'cs': {
    const mod = await import('date-fns/locale/cs')
    dateFnsLocale = mod.cs
    break
  }
  // ... existing cases (alphabetical by locale code) ...
  case 'ja': {                                         // ← Insert alphabetically
    const mod = await import('date-fns/locale/ja')
    dateFnsLocale = mod.ja
    break
  }
}
```

Check the [date-fns locale list](https://date-fns.org/docs/Locale) for available locale codes and export names. Most locales are available — if not, the app falls back to English date formatting.

### 5. Update System Locale Detection (if needed)

In `frontend/src/lib/i18n/index.ts`, the `detectSystemLocale()` function maps `navigator.language` to supported locales. For most languages, the automatic matching works (e.g., `ja-JP` matches `ja` via the language prefix).

If your language has regional variants that need special mapping (like Chinese: `zh` → `zh-TW`, `zh-HK` → `zh-HK`), add a case before the generic fallback:

```typescript
const lang = lower.split('-')[0]
if (lang === 'zh') return 'zh-TW'
// Add special cases here if needed
```

For most languages, no changes are needed here.

### 6. Translate Platform Files

These files are shown in Linux app stores and desktop environments. They need translated entries for your language.

#### A. AppStream Metainfo (`build/flatpak/io.github.hkdb.Aerion.metainfo.xml`)

This file is displayed in software centers (Flathub, GNOME Software). Add `xml:lang` variants for the following elements, placed directly after each English element:

- `<summary>` — one-line app description
- `<p>` blocks inside `<description>` — app description paragraphs
- `<li>` items inside `<description>` — feature list items
- `<caption>` inside `<screenshot>` — screenshot caption
- `<keyword>` entries — search keywords

**Example** (adding Japanese):
```xml
<summary>Lightweight open-source email client for Linux</summary>
<summary xml:lang="ja">Linux 向けの軽量オープンソースメールクライアント</summary>

<li>Multiple email accounts with unified inbox</li>
<li xml:lang="ja">統合受信箱を備えた複数メールアカウント対応</li>

<keyword>email</keyword>
<keyword xml:lang="ja">メール</keyword>
<keyword xml:lang="ja">電子メール</keyword>
```

Refer to the existing `cs`, `fr`, `zh-CN`, `zh-HK`, and `zh-TW` entries as working examples. Place new locale entries alphabetically by code (English first, the rest alphabetical).

#### B. Desktop Entry (`build/linux/aerion.desktop`)

This file is shown in app launchers and menus. Add `[locale_code]` suffixed keys. Note: desktop files use underscore (`_`) not hyphen for locale codes (e.g., `zh_TW` not `zh-TW`).

**Keys to translate:**

```ini
GenericName[ja]=メール
Comment[ja]=モダンなクロスプラットフォームメールクライアント
Keywords[ja]=メール;電子メール;IMAP;SMTP;CardDAV;

[Desktop Action compose]
Name[ja]=新規メール作成
```

### 7. Verify

```bash
cd frontend
npm run check    # Ensure no TypeScript errors
npm run build    # Ensure production build succeeds
```

Then run the app, open Settings > General, and select the new language from the Language dropdown. Verify:
- All strings in the UI are translated
- Dynamic strings with `{placeholders}` interpolate correctly (e.g., toast messages)
- Date formatting uses the correct locale
- The detached composer window also picks up the language

## File Summary

| File | Change |
|------|--------|
| `frontend/src/lib/i18n/locales/<code>.json` | **New** — translated strings (~900+ keys) |
| `frontend/src/lib/i18n/index.ts` | Add `register()` + `supportedLocales` entry |
| `frontend/src/lib/i18n/dateFnsLocale.ts` | Add `case` for date-fns locale |
| `build/flatpak/io.github.hkdb.Aerion.metainfo.xml` | Add `xml:lang` entries for app store listing |
| `build/linux/aerion.desktop` | Add `[locale]` suffixed keys for desktop integration |

No backend changes are needed. The language setting is stored via the existing `GetLanguage`/`SetLanguage` Wails bindings in `app/settings.go`.

## Existing Locales

| Code | Language | File |
|------|----------|------|
| `en` | English | `locales/en.json` (source of truth) |
| `cs` | Czech | `locales/cs.json` |
| `fr` | French | `locales/fr.json` |
| `zh-CN` | Simplified Chinese (China) | `locales/zh-CN.json` |
| `zh-HK` | Traditional Chinese (Hong Kong) | `locales/zh-HK.json` |
| `zh-TW` | Traditional Chinese (Taiwan) | `locales/zh-TW.json` |

## Translation Key Namespaces

| Namespace | Description |
|-----------|-------------|
| `common` | Shared buttons and labels (Save, Cancel, Delete, etc.) |
| `sidebar` | Sidebar navigation (Compose, All Inboxes, folder names) |
| `messageList` | Message list UI (select all, no messages, loading) |
| `viewer` | Message viewer (reply, forward, attachments, error states, S/MIME/PGP banners) |
| `composer` | Email composer (To, Cc, Subject, Send, formatting) |
| `contextMenu` | Right-click context menus (Reply, Archive, Mark as Read) |
| `toast` | Toast notification messages (clean translated messages without raw error details) |
| `responsive` | Responsive layout labels (back, folders) |
| `settings` | Settings dialog tabs and titles |
| `settingsAbout` | About tab in settings |
| `settingsAccounts` | Accounts tab in settings |
| `settingsGeneral` | General settings tab (theme, density, read receipts) |
| `editor` | TipTap editor toolbar labels |
| `account` | Account dialog and management |
| `identity` | Identity editor (email address management, display names, signatures) |
| `security` | S/MIME and PGP security settings |
| `contactSource` | CardDAV contact source management |
| `certificate` | TLS certificate trust dialog |
| `terms` | Terms of service dialog |
| `dialog` | Generic dialog strings (confirmations, warnings) |
| `date` | Date/time labels (just now, yesterday, etc.) |
| `aria` | Accessibility labels (screen reader text) |
| `window` | Window management (minimize, maximize, close) |
| `attachment` | Attachment handling (download, save, open) |
| `search` | Search UI |
| `sort` | Sort options (newest first, oldest first) |
| `oauth` | OAuth flow UI |

## Key Conventions

- **Error/failure messages**: Use clean, translated messages. Do not include raw error details or `{error}` interpolation tokens in failure messages — keep them user-friendly (e.g., `"Failed to save."` not `"Failed to save: {error}"`).
- **Placeholder tokens**: `{placeholder}` tokens are used for dynamic values. Common tokens include:
  - `{name}` — account or contact source name
  - `{email}` / `{emails}` — email address(es)
  - `{count}` — numeric count (messages, attachments, etc.)
  - `{mode}` — composer mode (reply, forward, etc.)
  - `{time}` — relative time string (from date-fns, already localized)
  - `{folder}` — folder name
  - `{percentage}` — sync progress percentage
  - `{version}` — app version string
  - `{provider}` — OAuth provider name (Google, Microsoft)
  - `{query}` — search query text
  - `{domain}` / `{sender}` — email domain or sender address
  - `{description}` — undo action description
  - `{filename}` — attachment filename
- **Token positioning**: Reposition tokens to match your language's grammar — don't assume English word order is correct for your language.
