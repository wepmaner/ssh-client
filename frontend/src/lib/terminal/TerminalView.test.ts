import { render } from '@testing-library/svelte'
import { flushSync } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// A stand-in xterm: records options and addons, so the test sees what TerminalView sets live.
const xterm = vi.hoisted(() => {
  class FakeTerminal {
    static last: FakeTerminal | undefined
    options: Record<string, unknown>
    addons: { dispose: () => void }[] = []
    cols = 80
    rows = 24
    constructor(options: Record<string, unknown>) {
      this.options = { ...options }
      FakeTerminal.last = this
    }
    loadAddon(a: { dispose: () => void }) {
      this.addons.push(a)
    }
    open() {}
    focus() {}
    write() {}
    paste() {}
    dispose() {}
    hasSelection() {
      return false
    }
    getSelection() {
      return ''
    }
    attachCustomKeyEventHandler() {}
    onData() {
      return { dispose() {} }
    }
    onBinary() {
      return { dispose() {} }
    }
    onResize() {
      return { dispose() {} }
    }
  }
  class FakeWebgl {
    static live = 0
    disposed = false
    constructor() {
      FakeWebgl.live++
    }
    onContextLoss() {}
    dispose() {
      if (!this.disposed) FakeWebgl.live--
      this.disposed = true
    }
  }
  class Plain {
    fit() {}
    dispose() {}
  }
  return { FakeTerminal, FakeWebgl, Plain }
})
vi.mock('@xterm/xterm', () => ({ Terminal: xterm.FakeTerminal }))
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: xterm.FakeWebgl }))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: xterm.Plain }))
vi.mock('@xterm/addon-search', () => ({ SearchAddon: xterm.Plain }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return {
    EventsOn: () => () => {},
    SessionResize: () => Promise.resolve(),
    SessionWrite: () => Promise.resolve(),
    ClipboardGetText: () => Promise.resolve(''),
    ClipboardSetText: () => Promise.resolve(),
    errorText: String,
    model,
  }
})

import { settings } from '../stores.svelte'
import TerminalView from './TerminalView.svelte'

describe('TerminalView settings', () => {
  beforeEach(() => {
    globalThis.ResizeObserver ??= class {
      observe() {}
      disconnect() {}
    } as never
    Object.assign(settings, { terminal_font: 'Cascadia Mono', terminal_size: 13, blink_cursor: true, webgl: true, copy_on_select: true })
  })

  it('applies font, size, cursor and renderer changes to an open terminal', () => {
    render(TerminalView, { sessionId: 's1', visible: true })
    const t = xterm.FakeTerminal.last!
    expect(t.options.fontFamily).toBe('"Cascadia Mono", Consolas, monospace')
    expect(xterm.FakeWebgl.live).toBe(1)

    settings.terminal_font = 'JetBrains Mono'
    settings.terminal_size = 18
    settings.blink_cursor = false
    flushSync()
    expect(t.options).toMatchObject({ fontFamily: '"JetBrains Mono", Consolas, monospace', fontSize: 18, cursorBlink: false })

    settings.webgl = false
    flushSync()
    expect(xterm.FakeWebgl.live).toBe(0)
    settings.webgl = true
    flushSync()
    expect(xterm.FakeWebgl.live).toBe(1)
  })
})
