// Forwards and their live state. Go sends fwd:state:<id> on every change of a forward. All forwards
// of a host share one connection: its state is fwdhost:state:<hostId>, and fwdhost:data:<hostId>
// carries the prompts of its login (passphrase, password, server questions) for the group's
// mini-terminal.
import {
  EventsOn,
  ForwardAttach,
  ForwardHostAttach,
  ForwardHostsList,
  ForwardStart,
  ForwardStartAll,
  ForwardStop,
  ForwardStopAll,
  ForwardsList,
  errorText,
  type app,
} from '../api'
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

/** Payload of fwdhost:state:<hostId>, see app.HostConnStateDTO. */
export interface HostStatePayload {
  status: string
  error: string
  detail?: string
  retryable: boolean
  asker: string
  session: string
}

export const forwards = $state<{ list: app.ForwardDTO[]; hosts: app.ForwardHostDTO[] }>({ list: [], hosts: [] })

/**
 * Bytes of mini-terminal output per host, kept while the host connects so a group that mounts
 * later still shows the pending prompt. outputSize makes "has output" reactive.
 */
const output = new Map<string, Uint8Array[]>()
const outputSink = new Map<string, (b: Uint8Array) => void>()
export const outputSize = $state<Record<string, number>>({})
const maxOutput = 64 << 10

const plain = <T>(v: T): T => JSON.parse(JSON.stringify(v))
const unsubscribe = new Map<string, () => void>()
const unsubscribeHost = new Map<string, () => void>()
/** State events seen per forward and per host: a list fetched meanwhile must not overwrite a newer event. */
const stateSeq = new Map<string, number>()
const hostSeq = new Map<string, number>()

function clearOutput(hostId: string): void {
  output.delete(hostId)
  delete outputSize[hostId]
}

function onData(hostId: string, b64: string): void {
  const b = decodeBase64(b64)
  const chunks = output.get(hostId) ?? []
  chunks.push(b)
  let size = (outputSize[hostId] ?? 0) + b.length
  while (size > maxOutput && chunks.length > 1) size -= chunks.shift()!.length
  output.set(hostId, chunks)
  outputSize[hostId] = size
  outputSink.get(hostId)?.(b)
}

function onHostState(hostId: string, p: HostStatePayload): void {
  hostSeq.set(hostId, (hostSeq.get(hostId) ?? 0) + 1)
  const h = forwards.hosts.find((x) => x.host_id === hostId)
  if (h) Object.assign(h, { status: p.status, error: p.error, detail: p.detail ?? '', retryable: p.retryable, asker: p.asker, session: p.session })
  // Prompts belong to one login: once it is over, the next one starts with a clean mini-terminal.
  if (p.status !== 'connecting') clearOutput(hostId)
}

function onState(id: string, p: ForwardStatePayload): void {
  stateSeq.set(id, (stateSeq.get(id) ?? 0) + 1)
  const f = forwards.list.find((x) => x.id === id)
  if (f) Object.assign(f, { status: p.status, error: p.error, detail: p.detail ?? '', conns: p.conns, retryable: p.retryable })
}

function subscribe(id: string): void {
  unsubscribe.set(id, EventsOn('fwd:state:' + id, (p: ForwardStatePayload) => onState(id, p)))
  // Go holds states until the UI listens, e.g. of an autostarted forward.
  void ForwardAttach(id).catch(() => {})
}

function subscribeHost(hostId: string): void {
  const offState = EventsOn('fwdhost:state:' + hostId, (p: HostStatePayload) => onHostState(hostId, p))
  const offData = EventsOn('fwdhost:data:' + hostId, (b64: string) => onData(hostId, b64))
  unsubscribeHost.set(hostId, () => {
    offState()
    offData()
  })
  // Go holds the login prompts and states until the UI listens.
  void ForwardHostAttach(hostId).catch(() => {})
}

/** Reloads the forwards and host groups and subscribes to the events of new ones. */
export async function refreshForwards(): Promise<void> {
  const seqBefore = new Map(stateSeq)
  const hostSeqBefore = new Map(hostSeq)
  const [rawList, rawHosts] = await Promise.all([ForwardsList(), ForwardHostsList()])
  const list = plain(rawList)
  const hostList = plain(rawHosts ?? [])
  for (const f of list) {
    const cur = forwards.list.find((x) => x.id === f.id)
    if (cur && stateSeq.get(f.id) !== seqBefore.get(f.id)) {
      Object.assign(f, { status: cur.status, error: cur.error, detail: cur.detail, conns: cur.conns, retryable: cur.retryable })
    }
  }
  for (const h of hostList) {
    const cur = forwards.hosts.find((x) => x.host_id === h.host_id)
    if (cur && hostSeq.get(h.host_id) !== hostSeqBefore.get(h.host_id)) {
      Object.assign(h, { status: cur.status, error: cur.error, detail: cur.detail, retryable: cur.retryable, asker: cur.asker, session: cur.session })
    }
  }
  forwards.list.splice(0, forwards.list.length, ...list)
  forwards.hosts.splice(0, forwards.hosts.length, ...hostList)
  const ids = new Set(list.map((f) => f.id))
  for (const [id, off] of unsubscribe) {
    if (!ids.has(id)) {
      off()
      unsubscribe.delete(id)
      stateSeq.delete(id)
    }
  }
  for (const id of ids) if (!unsubscribe.has(id)) subscribe(id)
  const hostIds = new Set(hostList.map((h) => h.host_id))
  for (const [id, off] of unsubscribeHost) {
    if (!hostIds.has(id)) {
      off()
      unsubscribeHost.delete(id)
      hostSeq.delete(id)
      clearOutput(id)
    }
  }
  for (const id of hostIds) if (!unsubscribeHost.has(id)) subscribeHost(id)
}

/**
 * Feeds a mini-terminal: first the output kept so far, then live output until the returned
 * function is called.
 */
export function attachOutput(hostId: string, sink: (b: Uint8Array) => void): () => void {
  for (const b of output.get(hostId) ?? []) sink(b)
  outputSink.set(hostId, sink)
  return () => {
    if (outputSink.get(hostId) === sink) outputSink.delete(hostId)
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

export async function startAll(hostId: string): Promise<void> {
  try {
    await ForwardStartAll(hostId)
  } catch (e) {
    toast(errorText(e))
  }
}

export async function stopAll(hostId: string): Promise<void> {
  try {
    await ForwardStopAll(hostId)
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
