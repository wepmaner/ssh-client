import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  KeysList: vi.fn(),
  KeyRename: vi.fn(),
  KeyDelete: vi.fn(),
  KeyCopyPublic: vi.fn(),
  KeyExport: vi.fn(),
  VaultRemovePassword: vi.fn(),
  VaultState: vi.fn(),
}))
vi.mock('../api', async () => {
  const models = await import('../../../wailsjs/go/models')
  return { ...api, errorText: String, model: models.model }
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

function row(name: string) {
  return within(screen.getByRole('list', { name: 'Ключи' })).getByRole('button', { name: new RegExp(name) })
}

describe('KeysView context menu', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    keys.splice(0, keys.length, ...(list as never[]))
    api.KeysList.mockResolvedValue(list)
  })

  it('right-click on a key selects it and offers rename and delete', async () => {
    render(KeysView)
    await fireEvent.contextMenu(row('home'), { clientX: 40, clientY: 50 })
    const menu = screen.getByRole('menu')
    expect(within(menu).getAllByRole('menuitem').map((b) => b.textContent)).toEqual(['Переименовать', 'Удалить'])
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('home')
  })

  it('rename from the menu opens the inline name editor of that key', async () => {
    render(KeysView)
    await fireEvent.contextMenu(row('home'))
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Переименовать' }))
    expect(screen.queryByRole('menu')).toBeNull()
    expect((screen.getByLabelText('Название ключа') as HTMLInputElement).value).toBe('home')
  })

  it('delete from the menu asks for confirmation first', async () => {
    render(KeysView)
    await fireEvent.contextMenu(row('work'))
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Удалить' }))
    expect(screen.getByRole('dialog', { name: 'Удалить ключ work?' })).toBeTruthy()
    expect(api.KeyDelete).not.toHaveBeenCalled()
  })
})
