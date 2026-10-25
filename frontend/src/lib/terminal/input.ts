// Wails runs every bound call in its own goroutine, so two SessionWrite calls in flight may
// reach Go in either order. The sender keeps one call in flight and coalesces the raw bytes typed
// meanwhile into the next call, which keeps input in order without delaying the first key.
import { encodeBase64 } from './base64'

export type Send = (b64: string) => Promise<unknown>

const utf8 = new TextEncoder()

/** Converts xterm input to bytes: onData strings are UTF-8 text, onBinary strings are one byte per char. */
export function inputBytes(data: string, binary = false): Uint8Array {
  if (!binary) return utf8.encode(data)
  const out = new Uint8Array(data.length)
  for (let i = 0; i < data.length; i++) out[i] = data.charCodeAt(i) & 0xff
  return out
}

/**
 * Returns a function that queues raw input bytes for send, in order. Queued chunks are
 * concatenated and base64-encoded once per call. Failed sends are reported and dropped.
 */
export function orderedSender(send: Send, onError: (e: unknown) => void = () => {}): (bytes: Uint8Array) => void {
  let pending: Uint8Array[] = []
  let inFlight = false

  async function pump(): Promise<void> {
    inFlight = true
    while (pending.length > 0) {
      const chunks = pending
      pending = []
      const total = chunks.reduce((n, c) => n + c.length, 0)
      const merged = new Uint8Array(total)
      let off = 0
      for (const c of chunks) {
        merged.set(c, off)
        off += c.length
      }
      try {
        await send(encodeBase64(merged))
      } catch (e) {
        onError(e)
      }
    }
    inFlight = false
  }

  return (bytes: Uint8Array) => {
    if (bytes.length === 0) return
    pending.push(bytes)
    if (!inFlight) void pump()
  }
}
