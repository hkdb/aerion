import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import nspell from 'nspell'
import { gunzipSync } from 'node:zlib'

async function dutchDictionary() {
  const [aff, dic] = await Promise.all([
    readFile(new URL('../node_modules/dictionary-nl/index.aff', import.meta.url), 'utf8'),
    readFile(new URL('../node_modules/dictionary-nl/index.dic', import.meta.url), 'utf8'),
  ])
  return nspell(aff, dic)
}

test('Dutch dictionary recognizes Dutch words and rejects a typo', async () => {
  const spell = await dutchDictionary()

  assert.equal(spell.correct('huis'), true)
  assert.equal(spell.correct('huiss'), false)
  assert.ok(spell.suggest('huiss').includes('huis'))
})

test('Dutch browser assets contain the dictionary files', async () => {
  const [aff, dic] = await Promise.all([
    readFile(new URL('../public/spellcheck/nl.aff.gz', import.meta.url)),
    readFile(new URL('../public/spellcheck/nl.dic.gz', import.meta.url)),
  ])

  assert.match(gunzipSync(aff).toString('utf8'), /Dutch support for Hunspell/)
  assert.match(gunzipSync(dic).toString('utf8'), /\nhuis-\n/)
})
