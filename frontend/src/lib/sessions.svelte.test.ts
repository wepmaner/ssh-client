import { beforeEach, describe, expect, it, vi } from 'vitest'

const { handlers, sessionOpen, sessionClose } = vi.hoisted(() => ({
  handlers: new Map<string, (p: unknown) => void>(),
  sessionOpen: vi.fn(),
  sessionClose: vi.fn(() => Promise.resolve()),
}))
vi.mock('./api', async () => {
  const model = await import('../../bindings/sshclient/internal/model/models')
  return {
    EventsOn: (name: string, cb: (p: unknown) => void) => {
      handlers.set(name, cb)
      return () => handlers.delete(name)
    },
    SessionOpen: sessionOpen,
    SessionClose: sessionClose,
    SessionList: () => Promise.resolve([]),
    GetSettings: vi.fn(),
    HostsList: vi.fn(),
    GroupsList: vi.fn(),
    KeysList: vi.fn(),
    VaultState: vi.fn(),
    errorText: String,
    model,
  }
})

import { activeSession, closeSession, cycleTab, newTab, openSession, reduceState, retrySession, sessions } from './sessions.svelte'
import { hosts, toastState, ui } from './stores.svelte'

beforeEach(() => {
  for (const t of [...sessions.list]) closeSession(t.id)
  handlers.clear()
  hosts.splice(0, hosts.length, { id: 'h1', name: 'prod', address: 'p', port: 22, user: 'u', group_id: '', group_name: '', key_id: '', key_name: '', os: '', mcp_access: '', connected: false })
  ui.activeTab = 'home'
  let n = 0
  sessionOpen.mockImplementation(() => Promise.resolve('s' + ++n))
})

describe('reduceState', () => {
  const tab = newTab('s', 'h1')

  it('moves through states and keeps error fields only in error', () => {
    const err = reduceState(tab, { state: 'error', kind: 'auth', message: 'm', detail: 'd', retryable: true })
    expect(err).toMatchObject({ state: 'error', kind: 'auth', message: 'm', detail: 'd', retryable: true })
    const again = reduceState(err, { state: 'connecting', retryable: true })
    expect(again).toMatchObject({ state: 'connecting', message: '', detail: '', kind: undefined })
  })

  it('records the exit code on close and forgets latency', () => {
    const closed = reduceState({ ...tab, state: 'connected', latencyMs: 12 }, { state: 'closed', exitCode: 3, retryable: true })
    expect(closed).toMatchObject({ state: 'closed', exitCode: 3, latencyMs: 0 })
  })

  it('defaults an unknown error kind to other', () => {
    expect(reduceState(tab, { state: 'error', retryable: false }).kind).toBe('other')
  })
})

describe('sessions store', () => {
  it('opens a tab, follows its state events and activates it', async () => {
    await openSession('h1')
    expect(sessions.list).toHaveLength(1)
    expect(sessions.list[0]).toMatchObject({ id: 's1', hostName: 'prod', state: 'connecting' })
    expect(activeSession()?.id).toBe('s1')
    handlers.get('term:state:s1')!({ state: 'connected', retryable: true })
    expect(sessions.list[0].state).toBe('connected')
  })

  it('shows a toast when SessionOpen fails', async () => {
    sessionOpen.mockRejectedValueOnce('Хост не найден')
    await openSession('nope')
    expect(sessions.list).toHaveLength(0)
    expect(toastState.message).toBe('Хост не найден')
  })

  it('cycles home and session tabs with Ctrl+Tab order', async () => {
    await openSession('h1')
    await openSession('h1')
    expect(activeSession()?.id).toBe('s2')
    cycleTab(1)
    expect(ui.activeTab).toBe('home')
    cycleTab(1)
    expect(activeSession()?.id).toBe('s1')
    cycleTab(-1)
    expect(ui.activeTab).toBe('home')
    cycleTab(-1)
    expect(activeSession()?.id).toBe('s2')
  })

  it('closing the active tab activates its neighbour, the last one goes home', async () => {
    await openSession('h1')
    await openSession('h1')
    closeSession('s2')
    expect(sessionClose).toHaveBeenCalledWith('s2')
    expect(handlers.has('term:state:s2')).toBe(false)
    expect(activeSession()?.id).toBe('s1')
    closeSession('s1')
    expect(ui.activeTab).toBe('home')
    expect(sessions.list).toHaveLength(0)
  })

  it('retry replaces the session in the same tab position', async () => {
    await openSession('h1')
    await openSession('h1')
    sessions.list[0].cols = 90
    handlers.get('term:state:s1')!({ state: 'error', kind: 'timeout', retryable: true })
    sessions.activeId = 's1'
    await retrySession('s1')
    expect(sessionOpen).toHaveBeenLastCalledWith('h1', 90, 32)
    expect(sessions.list.map((t) => t.id)).toEqual(['s3', 's2'])
    expect(sessions.list[0].state).toBe('connecting')
    expect(sessions.activeId).toBe('s3')
    expect(handlers.has('term:state:s1')).toBe(false)
    expect(handlers.has('term:state:s3')).toBe(true)
  })
})
