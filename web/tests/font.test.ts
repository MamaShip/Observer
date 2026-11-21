import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { expect, it } from 'vitest'

// The display font is a subset (scripts/subset-font.py). Collect characters
// the same way the script does, and require that it was run since.
const web = join(__dirname, '..')

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    return statSync(path).isDirectory() ? files(path) : [path]
  })
}

it('has a display-font subset covering the interface text', () => {
  const covered = new Set(readFileSync(join(web, 'src/assets/fonts/SmileySans-Oblique.chars.txt'), 'utf8').trimEnd())
  const missing = new Set<string>()
  for (const f of [join(web, 'index.html'), ...files(join(web, 'src'))]) {
    if (!/\.(vue|ts|html|css)$/.test(f)) continue
    for (const c of readFileSync(f, 'utf8')) {
      if (c.codePointAt(0)! > 0x7e && /\P{C}/u.test(c) && !covered.has(c)) missing.add(c)
    }
  }
  expect([...missing].join(''), 'run `make web-fonts`').toBe('')
})
