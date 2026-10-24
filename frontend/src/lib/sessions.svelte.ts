import { EventsOn, SessionClose, SessionList, SessionOpen, errorText } from './api'
import { hosts, toast, ui } from './stores.svelte'

export type SessionState = 'connecting' | 'connected' | 'error' | 'closed'
export type ErrorKind = 'timeout' | 'hostkey' | 'auth' | 'cancelled' | 'other'

/** Payload of term:state:<id>, see app.SessionStateDTO. */
export interface StatePayload {
  state: SessionState
  exitCode?: number
  message?: string
  detail?: string
  kind?: ErrorKind
  retryable: boolean
}

/** One session tab. cols/rows are the size the terminal view last reported. */
export interface SessionTab {
  id: string
  hostId: string
  hostName: string
  state: SessionState
  latencyMs: number
  exitCode?: number
  message: string
  detail: string
  kind?: ErrorKind
  retryable: boolean
  cols: number
  rows: number
}

/** Size used for SessionOpen before the view has measured itself. */
const initialCols = 120
const initialRows = 32
const latencyPollMs = 5000

export const sessions = $state<{ list: SessionTab[]; activeId: string; searchId: string }>({
  list: [],
  activeId: '',
  searchId: '',
})

/** Applies a term:state event to a tab; error fields live only while the state is error. */
export function reduceState(tab: SessionTab, p: StatePayload): SessionTab {
  const next: SessionTab = { ...tab, state: p.state, retryable: p.retryable, message: '', detail: '', kind: undefined, exitCode: undefined }
  if (p.state === 'error') {
    next.message = p.message ?? ''
    next.detail = p.detail ?? ''
    next.kind = p.kind ?? 'other'
  }
  if (p.state === 'closed') next.exitCode = p.exitCode
  if (p.state !== 'connected') next.latencyMs = 0
  return next
}

export function newTab(id: string, hostId: string): SessionTab {
  return {
    id,
    hostId,
    hostName: hosts.find((h) => h.id === hostId)?.name ?? '',
    state: 'connecting',
    latencyMs: 0,
    message: '',
    detail: '',
    retryable: true,
    cols: initialCols,
    rows: initialRows,
  }
}

const unsubscribe = new Map<string, () => void>()
let poll: ReturnType<typeof setInterval> | undefined

function subscribe(id: string): void {
  unsubscribe.set(
    id,
    EventsOn('term:state:' + id, (p: StatePayload) => {
      const i = sessions.list.findIndex((t) => t.id === id)
      if (i >= 0) sessions.list[i] = reduceState(sessions.list[i], p)
    }),
  )
}

function forget(id: string): void {
  unsubscribe.get(id)?.()
  unsubscribe.delete(id)
  if (sessions.searchId === id) sessions.searchId = ''
}

/** Polls SessionList for latency while any session is open. */
function syncPoll(): void {
  if (sessions.list.length > 0 && !poll) poll = setInterval(() => void refreshSessions(), latencyPollMs)
  if (sessions.list.length === 0 && poll) {
    clearInterval(poll)
    poll = undefined
  }
}

/** Merges latency and state from Go into the open tabs. */
export async function refreshSessions(): Promise<void> {
  const list = await SessionList()
  for (const tab of sessions.list) {
    const dto = list.find((d) => d.id === tab.id)
    if (dto) tab.latencyMs = dto.latency_ms
  }
}

export function activate(id: string): void {
  sessions.activeId = id
  ui.activeTab = 'session'
}

export function goHome(): void {
  ui.activeTab = 'home'
}

/** Opens a session tab for the host and brings it to front. */
export async function openSession(hostId: string): Promise<void> {
  try {
    const id = await SessionOpen(hostId, initialCols, initialRows)
    sessions.list.push(newTab(id, hostId))
    subscribe(id)
    activate(id)
    syncPoll()
  } catch (e) {
    toast(errorText(e))
  }
}

/** Closes the tab at once, without confirmation; the neighbour or the home tab comes to front. */
export function closeSession(id: string): void {
  const i = sessions.list.findIndex((t) => t.id === id)
  if (i < 0) return
  forget(id)
  void SessionClose(id).catch(() => {})
  sessions.list.splice(i, 1)
  if (sessions.activeId === id) {
    const next = sessions.list[i] ?? sessions.list[i - 1]
    sessions.activeId = next?.id ?? ''
    if (!next) goHome()
  }
  syncPoll()
}

/** Replaces the tab's session with a new one to the same host, in the same tab position. */
export async function retrySession(id: string): Promise<void> {
  const i = sessions.list.findIndex((t) => t.id === id)
  if (i < 0) return
  const old = sessions.list[i]
  forget(id)
  void SessionClose(id).catch(() => {})
  try {
    const nid = await SessionOpen(old.hostId, old.cols, old.rows)
    const j = sessions.list.findIndex((t) => t.id === id)
    if (j < 0) {
      void SessionClose(nid).catch(() => {})
      return
    }
    sessions.list[j] = { ...newTab(nid, old.hostId), cols: old.cols, rows: old.rows }
    subscribe(nid)
    if (sessions.activeId === id) sessions.activeId = nid
  } catch (e) {
    toast(errorText(e))
  }
}

/** Ctrl+Tab order: the home tab, then the session tabs. */
export function cycleTab(dir: 1 | -1): void {
  const order = ['', ...sessions.list.map((t) => t.id)]
  const cur = ui.activeTab === 'home' ? 0 : Math.max(0, order.indexOf(sessions.activeId))
  const next = order[(cur + dir + order.length) % order.length]
  if (next) activate(next)
  else goHome()
}

export function activeSession(): SessionTab | undefined {
  if (ui.activeTab !== 'session') return undefined
  return sessions.list.find((t) => t.id === sessions.activeId)
}
