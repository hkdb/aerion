// Gzips the hunspell .aff/.dic from the dictionary-* npm packages into
// public/spellcheck/<locale>.{aff,dic}.gz, so the spellcheck Web Worker can
// fetch them lazily and decompress with the native DecompressionStream. The npm
// packages' own loaders use node:fs and can't run in a browser worker, hence
// this build step. Gzipping cuts the bundled payload ~3x (word lists compress
// well). Runs on predev/prebuild; output is gitignored (regenerated).
import { mkdir, rm, readFile, writeFile } from 'node:fs/promises'
import { gzipSync } from 'node:zlib'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const scriptsDir = dirname(fileURLToPath(import.meta.url))
const frontend = dirname(scriptsDir)
const outDir = join(frontend, 'public', 'spellcheck')

// Latin-script locales Aerion ships dictionaries for. zh-* excluded (CJK has no
// Latin-style per-word spelling); vi omitted until a usable dictionary exists.
const LOCALES = ['en', 'cs', 'de', 'fr', 'it', 'nb', 'nl']

await rm(outDir, { recursive: true, force: true })
await mkdir(outDir, { recursive: true })
for (const locale of LOCALES) {
  const pkg = join(frontend, 'node_modules', `dictionary-${locale}`)
  for (const ext of ['aff', 'dic']) {
    const raw = await readFile(join(pkg, `index.${ext}`))
    await writeFile(join(outDir, `${locale}.${ext}.gz`), gzipSync(raw, { level: 9 }))
  }
}
console.log(`gzipped ${LOCALES.length} dictionaries -> ${outDir}`)
