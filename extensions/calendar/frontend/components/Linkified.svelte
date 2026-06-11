<script lang="ts">
  // Linkified — renders text with embedded http(s):// URLs as clickable
  // anchors. Calendar-domain helper used by EventDetail to make URLs in
  // event summary, location, and description fields openable in the
  // user's system browser.
  //
  // Clicks route through Calendar_OpenURL → coreapi.UI.OpenURL → the
  // host's hardened resolver (protocol allowlist, Linux portal-first,
  // xdg-open fallback). Never reaches Wails' BrowserOpenURL directly.
  //
  // The component is intentionally inline-only: each token renders as
  // an inline element so consumers can wrap it in any block container
  // (heading, paragraph, whitespace-pre-wrap div).

  import { logger } from '$extensions/calendar/frontend/lib/logger'
  // @ts-ignore - wailsjs bindings
  import { Calendar_OpenURL } from '$wailsjs/go/app/App.js'

  interface Props {
    text: string | undefined | null
  }

  let { text }: Props = $props()

  // Match http:// or https:// URLs. Greedy on non-whitespace/non-bracket
  // chars. Trailing punctuation is trimmed in tokenize() so a sentence
  // like "see https://example.com." doesn't include the period in the link.
  const URL_RE = /https?:\/\/[^\s<>"'()]+/g

  type Token = { kind: 'text' | 'url'; value: string }

  function tokenize(input: string): Token[] {
    const out: Token[] = []
    let lastIdx = 0
    for (const match of input.matchAll(URL_RE)) {
      const start = match.index ?? 0
      if (start > lastIdx) {
        out.push({ kind: 'text', value: input.slice(lastIdx, start) })
      }
      let url = match[0]
      let trailing = ''
      while (url.length > 0 && /[.,;:!?]/.test(url[url.length - 1])) {
        trailing = url[url.length - 1] + trailing
        url = url.slice(0, -1)
      }
      out.push({ kind: 'url', value: url })
      if (trailing) out.push({ kind: 'text', value: trailing })
      lastIdx = start + match[0].length
    }
    if (lastIdx < input.length) {
      out.push({ kind: 'text', value: input.slice(lastIdx) })
    }
    return out
  }

  const tokens = $derived(text ? tokenize(text) : [])

  function openLink(url: string, e: MouseEvent) {
    e.preventDefault()
    e.stopPropagation()
    Calendar_OpenURL(url).catch((err: unknown) => {
      logger.warn(`openURL failed: ${err}`)
    })
  }
</script>

{#each tokens as token, i (i)}
  {#if token.kind === 'text'}<span>{token.value}</span>{/if}
  {#if token.kind === 'url'}
    <a
      href={token.value}
      class="text-primary underline decoration-dotted underline-offset-2 hover:decoration-solid break-all"
      onclick={(e) => openLink(token.value, e)}
    >{token.value}</a>
  {/if}
{/each}
