// Forwards and their live state. Go sends fwd:state:<id> on every change and fwd:data:<id> with
// the prompts of a dial (passphrase, password, server questions) for the card's mini-terminal.
import { EventsOn, ForwardAttach, ForwardStart, ForwardStop, ForwardsList, errorText, type app } from '../api'
import { toast } from '../stores.svelte'
import { decodeBase64 } from '../terminal/base64'

export type ForwardStatus = 'stopped' | 'starting' | 'running' | 'error'

/** Payload of fwd:state:<id>, see app.ForwardStateDTO. */
export interface ForwardStatePayload {
  status: ForwardStatus
  error: string
  /** The system text under error (connectex, lookup …); may be absent. */
  detail?: string
  conns: number
  retryable: boolean
}

export const forwards = $state<{ list: app.ForwardDTO[] }>({ list: [] })

/**
 * Bytes of mini-terminal output per forward, kept while the forward is starting so a card that
 * mounts later still shows the pending prompt. outputSize makes "has output" reactive.
 */
const output = new Map<string, Uint8Array[]>()
const outputSink = new Map<string, (b: Uint8Array) => void>()
export const outputSize = $state<Record<string, number>>({})
const maxOutput = 64 << 10

const plain = <T>(v: T): T => JSON.parse(JSON.stringify(v))
const unsubscribe = new Map<string, () => void>()
/** State events seen per forward: a list fetched meanwhile must not overwrite a newer event. */
const stateSeq = new Map<string, number>()

function clearOutput(id: string): void {
  output.delete(id)
  delete outputSize[id]
}

function onData(id: string, b64: string): void {
  const b = decodeBase64(b64)
  const chunks = output.get(id) ?? []
  chunks.push(b)
  let size = (outputSize[id] ?? 0) + b.length
  while (size > maxOutput && chunks.length > 1) size -= chunks.shift()!.length
  output.set(id, chunks)
  outputSize[id] = size
  outputSink.get(id)?.(b)
}

function onState(id: string, p: ForwardStatePayload): void {
  stateSeq.set(id, (stateSeq.get(id) ?? 0) + 1)
  const f = forwards.list.find((x) => x.id === id)
  if (f) Object.assign(f, { status: p.status, error: p.error, detail: p.detail ?? '', conns: p.conns, retryable: p.retryable })
  // Prompts belong to one dial: once it is over, the next one starts with a clean mini-terminal.
  if (p.status !== 'starting') clearOutput(id)
}

function subscribe(id: string): void {
  const offState = EventsOn('fwd:state:' + id, (p: ForwardStatePayload) => onState(id, p))
  const offData = EventsOn('fwd:data:' + id, (b64: string) => onData(id, b64))
  unsubscribe.set(id, () => {
    offState()
    offData()
  })
  // Go holds output and states until the UI listens, e.g. the prompt of an autostarted forward.
  void ForwardAttach(id).catch(() => {})
}

/** Reloads the forwards and subscribes to the events of new ones. */
export async function refreshForwards(): Promise<void> {
  const seqBefore = new Map(stateSeq)
  const list = plain(await ForwardsList())
  for (const f of list) {
    const cur = forwards.list.find((x) => x.id === f.id)
    if (cur && stateSeq.get(f.id) !== seqBefore.get(f.id)) {
      Object.assign(f, { status: cur.status, error: cur.error, detail: cur.detail, conns: cur.conns, retryable: cur.retryable })
    }
  }
  forwards.list.splice(0, forwards.list.length, ...list)
  const ids = new Set(list.map((f) => f.id))
  for (const [id, off] of unsubscribe) {
    if (!ids.has(id)) {
      off()
      unsubscribe.delete(id)
      stateSeq.delete(id)
      clearOutput(id)
    }
  }
  for (const id of ids) if (!unsubscribe.has(id)) subscribe(id)
}

/**
 * Feeds a mini-terminal: first the output kept so far, then live output until the returned
 * function is called.
 */
export function attachOutput(id: string, sink: (b: Uint8Array) => void): () => void {
  for (const b of output.get(id) ?? []) sink(b)
  outputSink.set(id, sink)
  return () => {
    if (outputSink.get(id) === sink) outputSink.delete(id)
  }
}

export async function startForward(id: string): Promise<void> {
  try {
    await ForwardStart(id)
  } catch (e) {
    toast(errorText(e))
  }
}

export async function stopForward(id: string): Promise<void> {
  try {
    await ForwardStop(id)
  } catch (e) {
    toast(errorText(e))
  }
}

export function anyRunning(): boolean {
  return forwards.list.some((f) => f.status === 'running')
}

export function runningOfHost(hostId: string): number {
  return forwards.list.filter((f) => f.host_id === hostId && f.status === 'running').length
}
