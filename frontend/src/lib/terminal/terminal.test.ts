import { describe, expect, it, vi } from 'vitest'

vi.mock('../../../wailsjs/runtime/runtime', () => ({
  ClipboardGetText: vi.fn(),
  ClipboardSetText: vi.fn(),
}))

import { decodeBase64, encodeBase64 } from './base64'
import { inputBytes, orderedSender } from './input'
import { isAppShortcut, tabAction, terminalAction } from './keys'

const key = (init: KeyboardEventInit) => new KeyboardEvent('keydown', init)
const text = (b64: string) => new TextDecoder().decode(decodeBase64(b64))

describe('base64', () => {
  it('round-trips raw bytes', () => {
    const bytes = new Uint8Array([0x00, 0xff, 0x1b, 0x5b, 0xd0, 0xbf])
    expect(Array.from(decodeBase64(encodeBase64(bytes)))).toEqual(Array.from(bytes))
    expect(Array.from(decodeBase64('AP8bWw=='))).toEqual([0x00, 0xff, 0x1b, 0x5b])
  })

  it('encodes large pastes', () => {
    expect(atob(encodeBase64(new Uint8Array(200_000))).length).toBe(200_000)
  })
})

describe('inputBytes', () => {
  it('encodes onData text as UTF-8 and onBinary strings byte per char', () => {
    expect(new TextDecoder().decode(inputBytes('привет\r'))).toBe('привет\r')
    expect(Array.from(inputBytes('\x1b[M\xff\x80', true))).toEqual([0x1b, 0x5b, 0x4d, 0xff, 0x80])
  })
})

describe('orderedSender', () => {
  it('keeps one call in flight and merges input typed meanwhile into one valid payload', async () => {
    const calls: string[] = []
    const resolvers: (() => void)[] = []
    const send = orderedSender((b64) => {
      calls.push(b64)
      return new Promise<void>((r) => resolvers.push(r))
    })
    send(inputBytes('a'))
    send(inputBytes('b'))
    send(inputBytes('щ'))
    send(inputBytes('c'))
    expect(calls.map(text)).toEqual(['a'])
    resolvers.shift()!()
    await vi.waitFor(() => expect(calls).toHaveLength(2))
    expect(text(calls[1])).toBe('bщc')
    resolvers.shift()!()
    await Promise.resolve()
    send(inputBytes('d'))
    await vi.waitFor(() => expect(calls.map(text)).toEqual(['a', 'bщc', 'd']))
  })

  it('merges an onBinary chunk with an onData chunk byte for byte', async () => {
    const calls: string[] = []
    let release = () => {}
    const send = orderedSender((b64) => {
      calls.push(b64)
      return new Promise<void>((r) => (release = r))
    })
    send(inputBytes('x'))
    send(inputBytes('\x1b[M\xff', true))
    send(inputBytes('é'))
    release()
    await vi.waitFor(() => expect(calls).toHaveLength(2))
    expect(Array.from(decodeBase64(calls[1]))).toEqual([0x1b, 0x5b, 0x4d, 0xff, 0xc3, 0xa9])
  })

  it('reports a failed send and keeps going', async () => {
    const errors: unknown[] = []
    const calls: string[] = []
    const send = orderedSender(
      (b64) => {
        calls.push(text(b64))
        return text(b64) === 'x' ? Promise.reject(new Error('gone')) : Promise.resolve()
      },
      (e) => errors.push(e),
    )
    send(inputBytes('x'))
    send(inputBytes('y'))
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
