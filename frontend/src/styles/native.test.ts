import { describe, expect, it } from 'vitest'
import dialog from '../lib/ui/Dialog.svelte?raw'
import input from '../lib/ui/Input.svelte?raw'
import titlebar from '../lib/titlebar/TitleBar.svelte?raw'
// Not empty only because vite.config.ts lets Vitest process this CSS file.
import tokens from './tokens.css?raw'

// CLAUDE.md «Правила нативного поведения»: the motion and focus rules are CSS, which jsdom
// does not apply, so these tests read the sources.

const components = import.meta.glob('../lib/**/*.svelte', { query: '?raw', import: 'default', eager: true }) as Record<
  string,
  string
>

function block(css: string, selector: RegExp): string {
  const m = new RegExp(selector.source + String.raw`\s*\{([^}]*)\}`).exec(css)
  return m?.[1] ?? ''
}

/** The innermost rules of a component's <style>: selector and body, comments dropped. */
function rules(src: string): { selector: string; body: string }[] {
  const style = /<style[^>]*>([\s\S]*?)<\/style>/.exec(src)?.[1] ?? ''
  const css = style.replace(/\/\*[\s\S]*?\*\//g, '')
  return [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map((m) => ({ selector: m[1].trim(), body: m[2] }))
}

const field = /(^|[\s,>+~(])(input|select|textarea)\b|\.(input|select)\b/
const menuFile = '../lib/ui/Menu.svelte'

describe('native motion and focus', () => {
  it('has the focus ring colour and a global keyboard focus ring', () => {
    expect(tokens).toMatch(/--focus:\s*#5b9cf8/i)
    expect(block(tokens, /(?:^|\n):focus-visible/)).toMatch(/outline:\s*2px solid var\(--focus\)/)
  })

  // Ruling 2026-10-25: fields show focus with the accent border, menu items with the highlight,
  // every other control with the 2px var(--focus) ring.
  it('draws every focus outline in the focus colour', () => {
    for (const [file, src] of Object.entries(components)) {
      for (const m of src.matchAll(/outline:\s*2px solid ([^;]+);/g)) expect(`${file}: ${m[1]}`).toBe(`${file}: var(--focus)`)
    }
  })

  it('removes the ring only from fields, which show focus with the accent border, and from menu items', () => {
    for (const [file, src] of Object.entries(components)) {
      const list = rules(src)
      const removed = list.filter((r) => /outline:\s*(none|0)\b/.test(r.body))
      for (const r of removed) {
        if (file === menuFile) {
          expect(r.selector, file).toMatch(/button:focus-visible/)
          expect(r.body, file).toMatch(/background:/)
          continue
        }
        expect(r.selector, `${file}: ${r.selector}`).toMatch(field)
      }
      if (removed.length > 0 && file !== menuFile) {
        const border = list.some((r) => /:focus|:focus-within/.test(r.selector) && /border(-\w+)?-color:\s*var\(--accent\)/.test(r.body))
        expect(border, `${file} has no accent border on focus`).toBe(true)
      }
    }
    expect(block(input, /\.input:focus/)).toMatch(/border-color:\s*var\(--accent\)/)
  })

  it('keeps the standard ring on every other focus-visible rule', () => {
    for (const [file, src] of Object.entries(components)) {
      if (file === menuFile) continue
      for (const r of rules(src).filter((r) => r.selector.includes(':focus-visible'))) {
        const outline = /outline:\s*([^;]+);/.exec(r.body)?.[1]
        if (outline !== undefined) expect(`${file}: ${outline}`).toBe(`${file}: 2px solid var(--focus)`)
      }
    }
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
    const button = [...titlebar.matchAll(/<button\b[^>]*>/g)].map((m) => m[0]).find((b) => b.includes('aria-label="Развернуть"'))
    expect(button).toBeDefined()
    const classes = /\bclass="([^"]*)"/.exec(button!)?.[1].split(/\s+/) ?? []
    expect(classes).toContain('max')
    expect(block(titlebar, /\.max/)).toMatch(/--wails-non-client-region:\s*maximize/)
  })

  // Hover is 80 ms everywhere. The forwards screen has the approved exceptions of its design: the
  // switch (120 ms), the dots and the status pill (160 ms), the login block that collapses (220 ms)
  // and the line «Вход выполнен» (180 ms, 220 ms).
  const longer: Record<string, string[]> = {
    '../lib/forwards/ForwardCard.svelte': ['120ms', '160ms'],
    '../lib/forwards/ForwardGroup.svelte': ['120ms', '160ms', '180ms', '220ms'],
  }

  it('uses 80 ms hover transitions only, besides the approved ones of the forwards screen', () => {
    for (const [file, src] of Object.entries(components)) {
      for (const m of src.matchAll(/transition(?:-duration)?:([^;]*);/g)) {
        for (const d of m[1].matchAll(/\b(\d*\.?\d+)(ms|s)\b/g)) {
          const ok = d[0] === '80ms' || (longer[file] ?? []).includes(d[0])
          expect(`${file}: ${ok ? '80ms' : d[0]}`).toBe(`${file}: 80ms`)
        }
      }
    }
  })
})
