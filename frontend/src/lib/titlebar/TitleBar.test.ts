import { fireEvent, render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { handlers, api } = vi.hoisted(() => ({
  handlers: new Map<string, (p: unknown) => void>(),
  api: {
    SessionOpen: vi.fn(),
    SessionClose: vi.fn(),
    SessionList: vi.fn(),
    FilesOpen: vi.fn(),
    FilesClose: vi.fn(),
    FilesHome: vi.fn(),
    FilesLoginAttach: vi.fn(),
    WindowClose: vi.fn(),
    WindowMinimise: vi.fn(),
    WindowToggleMaximise: vi.fn(),
  },
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return {
    ...api,
    EventsOn: (name: string, cb: (p: unknown) => void) => {
      handlers.set(name, cb)
      return () => handlers.delete(name)
    },
    errorText: String,
    model,
    GetSettings: vi.fn(),
    HostsList: vi.fn(),
    GroupsList: vi.fn(),
    KeysList: vi.fn(),
    VaultState: vi.fn(),
  }
})

import { closeSession, openSession, sessions } from '../sessions.svelte'
import { hosts } from '../stores.svelte'
import TitleBar from './TitleBar.svelte'

beforeEach(() => {
  for (const t of [...sessions.list]) closeSession(t.id)
  vi.resetAllMocks()
  hosts.splice(0, hosts.length, { id: 'h1', name: 'prod', address: 'p', port: 22, user: 'u', group_id: '', group_name: '', key_id: '', key_name: '', os: '', mcp_access: '', connected: false })
  api.SessionOpen.mockResolvedValue('s1')
  api.SessionClose.mockResolvedValue(undefined)
  api.FilesOpen.mockResolvedValue('f1')
  api.FilesClose.mockResolvedValue(undefined)
  api.FilesHome.mockRejectedValue('нет подключения')
  api.FilesLoginAttach.mockResolvedValue(undefined)
})

describe('TitleBar with file tabs', () => {
  it('opens the files of the console host and shows the transfer percent on the tab', async () => {
    await openSession('h1')
    render(TitleBar, { title: 'Хосты', tabs: true })
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Файлы' }))
    await tick()
    expect(api.FilesOpen).toHaveBeenCalledWith('h1')
    const tab = screen.getByRole('tab', { name: /Файлы prod/ })
    expect(tab.getAttribute('aria-selected')).toBe('true')
    // The terminal actions belong to terminal tabs only.
    expect(screen.queryByRole('button', { name: 'Поиск в терминале' })).toBeNull()

    handlers.get('files:job:f1')!({ id: 'j', kind: 'upload', title: 'a', state: 'running', message: '', detail: '', total_bytes: 200, done_bytes: 90, total_files: 1, done_files: 0, failed: 0, renamed: 0, current: '', speed_bps: 0, dest: '', retry_in_s: 0 })
    await tick()
    expect(tab.textContent).toContain('prod · 45%')
  })
})
