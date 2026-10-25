import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

type Handler = (p: unknown) => void
const menuHandlers = vi.hoisted(() => [] as Handler[])
const api = vi.hoisted(() => ({
  EventsOn: vi.fn(),
  KeysList: vi.fn(),
  KeyRename: vi.fn(),
  KeyDelete: vi.fn(),
  KeyCopyPublic: vi.fn(),
  KeyExport: vi.fn(),
  VaultRemovePassword: vi.fn(),
  VaultState: vi.fn(),
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})

import { keys } from '../stores.svelte'
import KeysView from './KeysView.svelte'

const base = {
  type: 'ed25519',
  format: 'openssh',
  encrypted: false,
  public_line: 'ssh-ed25519 AAAA c',
  comment: 'c',
  fingerprint: 'SHA256:x',
  source: 'generated',
  created_at: '2026-10-24T00:00:00Z',
  host_ids: [] as string[],
}
const list = [
  { ...base, id: 'k1', name: 'work' },
  { ...base, id: 'k2', name: 'home' },
]

/** Emits menu:action as Go does after a click in a native context menu. */
function menuAction(action: string, id: string) {
  for (const h of menuHandlers) h({ menu: 'key', action, id })
}

function row(name: string) {
  return within(screen.getByRole('list', { name: 'Ключи' })).getByRole('button', { name: new RegExp(name) })
}

describe('KeysView context menu', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    menuHandlers.length = 0
    api.EventsOn.mockImplementation((name: string, h: Handler) => {
      if (name === 'menu:action') menuHandlers.push(h)
      return () => menuHandlers.splice(menuHandlers.indexOf(h), 1)
    })
    keys.splice(0, keys.length, ...(list as never[]))
    api.KeysList.mockResolvedValue(list)
  })

  it('right-click on a key selects it and opens the native key menu', async () => {
    render(KeysView)
    const home = row('home')
    expect(home.style.getPropertyValue('--custom-contextmenu')).toBe('key')
    expect(home.style.getPropertyValue('--custom-contextmenu-data')).toBe('k2')
    await fireEvent.contextMenu(home, { clientX: 40, clientY: 50 })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('home')
  })

  it('rename from the native menu opens the inline name editor of that key', async () => {
    render(KeysView)
    await menuAction('rename', 'k2')
    expect(((await screen.findByLabelText('Название ключа')) as HTMLInputElement).value).toBe('home')
  })

  it('delete from the native menu asks for confirmation first', async () => {
    render(KeysView)
    await fireEvent.click(row('home'))
    await menuAction('delete', 'k1')
    expect(await screen.findByRole('dialog', { name: 'Удалить ключ work?' })).toBeTruthy()
    expect(api.KeyDelete).not.toHaveBeenCalled()
  })
})
