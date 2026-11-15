import { EventsOn, FilesClose, FilesOpen, SessionClose, SessionList, SessionOpen, errorText } from './api'
import { createFilesTab, disposeFilesTab, filesTab } from './files/files.svelte'
import { hosts, toast, ui } from './stores.svelte'

export type SessionState = 'connecting' | 'connected' | 'error' | 'closed'
export type ErrorKind = 'timeout' | 'hostkey' | 'auth' | 'cancelled' | 'other'
/** What a tab shows: a terminal or the host's files over SFTP. */
export type TabType = 'term' | 'files'

/** Payload of term:state:<id>, see app.SessionStateDTO. */
export interface StatePayload {
  state: SessionState
  exitCode?: number
  message?: string
  detail?: string
  kind?: ErrorKind
  retryable: boolean
}

/** One tab of the title bar. cols/rows are the size the terminal view last reported. */
export interface SessionTab {
  id: string
  type: TabType
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

/** closeAsk is the file tab whose close waits for «Остановить передачи и закрыть?». */
export const sessions = $state<{ list: SessionTab[]; activeId: string; searchId: string; closeAsk: string }>({
  list: [],
  activeId: '',
  searchId: '',
  closeAsk: '',
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

export function newTab(id: string, hostId: string, type: TabType = 'term'): SessionTab {
  return {
    id,
    type,
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

function subscribe(id: string, type: TabType = 'term'): void {
  unsubscribe.set(
    id,
    EventsOn((type === 'files' ? 'files:state:' : 'term:state:') + id, (p: StatePayload) => {
      const i = sessions.list.findIndex((t) => t.id === id)
      if (i >= 0) sessions.list[i] = reduceState(sessions.list[i], p)
      if (type === 'files') filesTab(id)?.onState(p.state)
    }),
  )
}

function forget(id: string): void {
  unsubscribe.get(id)?.()
  unsubscribe.delete(id)
  if (sessions.searchId === id) sessions.searchId = ''
  if (sessions.closeAsk === id) sessions.closeAsk = ''
}

/** Polls SessionList for latency while any terminal is open. */
function syncPoll(): void {
  const terms = sessions.list.some((t) => t.type === 'term')
  if (terms && !poll) poll = setInterval(() => void refreshSessions().catch(() => {}), latencyPollMs)
  if (!terms && poll) {
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

/**
 * Opens the files tab of the host, or brings its open one to front: one files tab per host,
 * which already shows every folder of it.
 */
export async function openFiles(hostId: string): Promise<void> {
  const open = sessions.list.find((t) => t.type === 'files' && t.hostId === hostId)
  if (open) {
    activate(open.id)
    return
  }
  try {
    const id = await FilesOpen(hostId)
    // A probe that finds the host's connection already up marks the tab connected.
    const markConnected = () => {
      const t = sessions.list.find((x) => x.id === id)
      if (t && t.state === 'connecting') Object.assign(t, reduceState(t, { state: 'connected', retryable: true }))
    }
    sessions.list.push(newTab(id, hostId, 'files'))
    subscribe(id, 'files')
    createFilesTab(id, hostId, markConnected)
    activate(id)
  } catch (e) {
    toast(errorText(e))
  }
}

/** A close the user asked for: a file tab with transfers under way asks first. */
export function requestClose(id: string): void {
  const tab = sessions.list.find((t) => t.id === id)
  if (tab?.type === 'files' && filesTab(id)?.busy) sessions.closeAsk = id
  else closeSession(id)
}

/** Closes the tab at once, without confirmation; the neighbour or the home tab comes to front. */
export function closeSession(id: string): void {
  const i = sessions.list.findIndex((t) => t.id === id)
  if (i < 0) return
  forget(id)
  if (sessions.list[i].type === 'files') {
    disposeFilesTab(id)
    void FilesClose(id).catch(() => {})
  } else void SessionClose(id).catch(() => {})
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
  if (old.type !== 'term') return
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

/** How to focus each mounted terminal, registered by TerminalView. */
const terminalFocus = new Map<string, () => void>()

/** Registers the focus function of a session's terminal; returns the unregister function. */
export function registerTerminalFocus(id: string, focus: () => void): () => void {
  terminalFocus.set(id, focus)
  return () => {
    if (terminalFocus.get(id) === focus) terminalFocus.delete(id)
  }
}

/** Focuses the terminal of session id if it is mounted; false otherwise. */
export function focusTerminal(id: string): boolean {
  const focus = terminalFocus.get(id)
  focus?.()
  return focus !== undefined
}
