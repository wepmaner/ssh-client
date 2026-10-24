import { describe, expect, it, vi } from 'vitest'

vi.mock('../../../wailsjs/runtime/runtime', () => ({
  ClipboardGetText: vi.fn(),
  ClipboardSetText: vi.fn(),
}))

import { decodeBase64, encodeUtf8Base64 } from './base64'
import { orderedSender } from './input'
import { isAppShortcut, tabAction, terminalAction } from './keys'

const key = (init: KeyboardEventInit) => new KeyboardEvent('keydown', init)

describe('base64', () => {
  it('round-trips UTF-8 input as bytes', () => {
    const b64 = encodeUtf8Base64('привет\r')
    expect(new TextDecoder().decode(decodeBase64(b64))).toBe('привет\r')
  })

  it('decodes raw bytes without text conversion', () => {
    expect(Array.from(decodeBase64('AP8bWw=='))).toEqual([0x00, 0xff, 0x1b, 0x5b])
  })

  it('encodes large pastes', () => {
    const big = 'x'.repeat(200_000)
    expect(atob(encodeUtf8Base64(big)).length).toBe(200_000)
  })
})

describe('orderedSender', () => {
  it('keeps one call in flight and coalesces input typed meanwhile', async () => {
    const calls: string[] = []
    const resolvers: (() => void)[] = []
    const send = orderedSender((d) => {
      calls.push(d)
      return new Promise<void>((r) => resolvers.push(r))
    })
    send('a')
    send('b')
    send('c')
    expect(calls).toEqual(['a'])
    resolvers.shift()!()
    await vi.waitFor(() => expect(calls).toEqual(['a', 'bc']))
    resolvers.shift()!()
    await Promise.resolve()
    send('d')
    await vi.waitFor(() => expect(calls).toEqual(['a', 'bc', 'd']))
  })

  it('reports a failed send and keeps going', async () => {
    const errors: unknown[] = []
    const calls: string[] = []
    const send = orderedSender(
      (d) => {
        calls.push(d)
        return d === 'x' ? Promise.reject(new Error('gone')) : Promise.resolve()
      },
      (e) => errors.push(e),
    )
    send('x')
    send('y')
    await vi.waitFor(() => expect(calls).toEqual(['x', 'y']))
    expect(errors).toHaveLength(1)
  })
})

describe('keys', () => {
  it('maps terminal shortcuts by physical key', () => {
    expect(terminalAction(key({ ctrlKey: true, shiftKey: true, code: 'KeyC', key: 'С' }))).toBe('copy')
    expect(terminalAction(key({ ctrlKey: true, shiftKey: true, code: 'KeyV', key: 'М' }))).toBe('paste')
    expect(terminalAction(key({ ctrlKey: true, code: 'KeyF', key: 'а' }))).toBe('search')
    expect(terminalAction(key({ ctrlKey: true, code: 'KeyC', key: 'c' }))).toBeNull() // Ctrl+C goes to the shell
    expect(terminalAction(new KeyboardEvent('keyup', { ctrlKey: true, shiftKey: true, code: 'KeyC' }))).toBeNull()
  })

  it('maps tab shortcuts', () => {
    expect(tabAction(key({ ctrlKey: true, key: 'Tab' }))).toBe('next')
    expect(tabAction(key({ ctrlKey: true, shiftKey: true, key: 'Tab' }))).toBe('prev')
    expect(tabAction(key({ ctrlKey: true, code: 'KeyW', key: 'ц' }))).toBe('close')
    expect(tabAction(key({ key: 'Tab' }))).toBeNull()
    expect(isAppShortcut(key({ ctrlKey: true, code: 'KeyD', key: 'd' }))).toBe(false)
  })
})
