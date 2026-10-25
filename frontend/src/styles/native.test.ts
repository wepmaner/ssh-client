import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import dialog from '../lib/ui/Dialog.svelte?raw'
import titlebar from '../lib/titlebar/TitleBar.svelte?raw'

// CLAUDE.md «Правила нативного поведения»: the motion and focus rules are CSS, which jsdom
// does not apply, so these tests read the sources.

// Vitest empties CSS imports, even with ?raw: read the file itself.
const tokens = readFileSync('src/styles/tokens.css', 'utf8') // vitest runs in frontend/

const components = import.meta.glob('../lib/**/*.svelte', { query: '?raw', import: 'default', eager: true }) as Record<
  string,
  string
>

// Locked by another review while Task 15 ran; its link outline moves to var(--focus) in a follow-up.
const pendingFocus = new Set(['../lib/hosts/HostDialog.svelte'])

function block(css: string, selector: RegExp): string {
  const m = new RegExp(selector.source + String.raw`\s*\{([^}]*)\}`).exec(css)
  return m?.[1] ?? ''
}

describe('native motion and focus', () => {
  it('has the focus ring colour and a global keyboard focus ring', () => {
    expect(tokens).toMatch(/--focus:\s*#5b9cf8/i)
    expect(block(tokens, /(?:^|\n):focus-visible/)).toMatch(/outline:\s*2px solid var\(--focus\)/)
  })

  it('turns off transitions and animations when the system asks for reduced motion', () => {
    const m = /@media \(prefers-reduced-motion: reduce\)\s*\{([\s\S]*?\})\s*\}/.exec(tokens)
    expect(m).not.toBeNull()
    expect(m![1]).toMatch(/transition:\s*none\s*!important/)
    expect(m![1]).toMatch(/animation:\s*none\s*!important/)
  })

  it('opens a dialog in 150 ms from scale 0.97 and skips it under reduced motion', () => {
    expect(block(dialog, /\.panel/)).toMatch(/animation:\s*enter 150ms/)
    expect(dialog).toMatch(/scale\(0\.97\)/)
    expect(dialog).toMatch(/prefers-reduced-motion: reduce/)
  })

  it('switches tabs without animation', () => {
    const tab = block(titlebar, /\n {2}\.tab/)
    expect(tab).not.toBe('')
    expect(tab).not.toMatch(/transition|animation/)
  })

  it('marks the maximise button as a non-client region for the Snap layouts', () => {
    expect(titlebar).toMatch(/class="btn max" aria-label="Развернуть"/)
    expect(block(titlebar, /\.max/)).toMatch(/--wails-non-client-region:\s*maximize/)
  })

  it('uses 80 ms hover transitions only', () => {
    for (const [file, src] of Object.entries(components)) {
      for (const m of src.matchAll(/transition:([^;]*);/g)) {
        for (const d of m[1].matchAll(/(\d+)m?s\b/g)) expect(`${file}: ${d[0]}`).toBe(`${file}: 80ms`)
      }
    }
  })

  it('draws every focus outline in the focus colour', () => {
    for (const [file, src] of Object.entries(components)) {
      if (pendingFocus.has(file)) continue
      expect(src, file).not.toMatch(/outline:\s*2px solid var\(--accent\)/)
    }
  })
})
