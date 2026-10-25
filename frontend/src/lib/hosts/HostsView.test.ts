import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

type Handler = (p: unknown) => void
const menuHandlers = vi.hoisted(() => [] as Handler[])
const api = vi.hoisted(() => ({
  EventsOn: vi.fn(),
  HostSave: vi.fn(),
  HostDelete: vi.fn(),
  GroupSave: vi.fn(),
  GroupRename: vi.fn(),
  GroupDelete: vi.fn(),
  HostsList: vi.fn(),
  GroupsList: vi.fn(),
  KeysList: vi.fn(),
  GetSettings: vi.fn(),
  VaultState: vi.fn(),
  KeyImportFile: vi.fn(),
  KeyImportFileWithPassphrase: vi.fn(),
  KeyImportText: vi.fn(),
  KeyImportTextWithPassphrase: vi.fn(),
}))
const sess = vi.hoisted(() => ({
  openSession: vi.fn(),
  closeSession: vi.fn(),
  sessions: { list: [] as { id: string; hostId: string; state: string }[] },
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})
vi.mock('../sessions.svelte', () => sess)

import { groups, hosts, ui } from '../stores.svelte'
import HostsView from './HostsView.svelte'

const base = { group_id: '', group_name: '', key_id: 'k1', key_name: 'work', port: 22, os: '', connected: false }
const list = [
  { ...base, id: 'h1', name: 'prod-1', address: '10.0.4.11', user: 'deploy', os: 'ubuntu' },
  { ...base, id: 'h2', name: 'db-01', address: 'db.example.test', user: 'root', os: 'debian' },
  { ...base, id: 'h3', name: 'nas', address: '127.0.0.1', user: 'admin' },
]

function load(items: typeof list) {
  hosts.splice(0, hosts.length, ...(items as never[]))
  api.HostsList.mockResolvedValue(items)
}

const names = () => screen.queryAllByTestId('host-name').map((e) => e.textContent)

/** Emits menu:action as Go does after a click in a native context menu. */
async function menuAction(menu: string, action: string, id: string) {
  for (const h of menuHandlers) h({ menu, action, id })
  await Promise.resolve()
}

/** The CSS custom properties the Wails runtime reads on right-click. */
function nativeMenu(el: HTMLElement | null): [string, string] {
  const tile = el?.closest<HTMLElement>('[role="listitem"]')
  return [tile?.style.getPropertyValue('--custom-contextmenu') ?? '', tile?.style.getPropertyValue('--custom-contextmenu-data') ?? '']
}

describe('HostsView', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    menuHandlers.length = 0
    api.EventsOn.mockImplementation((name: string, h: Handler) => {
      if (name === 'menu:action') menuHandlers.push(h)
      return () => menuHandlers.splice(menuHandlers.indexOf(h), 1)
    })
    groups.splice(0, groups.length)
    api.GroupsList.mockResolvedValue([])
    api.KeysList.mockResolvedValue([])
    sess.sessions.list = []
    ui.activeTab = 'home'
    ui.editHostId = ''
  })

  it('shows the first-run screen when there are no hosts', async () => {
    load([])
    render(HostsView)
    expect(screen.getByRole('heading', { name: 'Хостов пока нет' })).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Добавить хост' }))
    expect(screen.getByRole('dialog', { name: 'Новый хост' })).toBeTruthy()
  })

  it('filters by name, address and user', async () => {
    load(list)
    render(HostsView)
    expect(names()).toEqual(['prod-1', 'db-01', 'nas'])
    const search = screen.getByLabelText('Поиск хостов по имени, адресу или пользователю')
    await fireEvent.input(search, { target: { value: 'DB' } })
    expect(names()).toEqual(['db-01'])
    await fireEvent.input(search, { target: { value: '10.0.4' } })
    expect(names()).toEqual(['prod-1'])
    await fireEvent.input(search, { target: { value: 'admin' } })
    expect(names()).toEqual(['nas'])
    await fireEvent.input(search, { target: { value: 'zzz' } })
    expect(names()).toEqual([])
    expect(screen.getByText('Ничего не найдено')).toBeTruthy()
  })

  it('Ctrl+K focuses the search', async () => {
    load(list)
    render(HostsView)
    await fireEvent.keyDown(window, { key: 'k', ctrlKey: true })
    expect(document.activeElement).toBe(screen.getByLabelText('Поиск хостов по имени, адресу или пользователю'))
  })

  it('a tile click opens a session', async () => {
    load(list)
    render(HostsView)
    await fireEvent.click(screen.getByRole('button', { name: /^nas/ }))
    expect(sess.openSession).toHaveBeenCalledWith('h3')
  })

  it('shows the OS monogram', () => {
    load(list)
    render(HostsView)
    expect(screen.getAllByTestId('monogram').map((e) => e.textContent)).toEqual(['Ub', 'Db', 'Na'])
  })

  it('the list layout shows the same hosts and is remembered', async () => {
    load(list)
    const first = render(HostsView)
    await fireEvent.click(screen.getByRole('button', { name: 'Список' }))
    expect(screen.getByRole('button', { name: 'Список' }).getAttribute('aria-pressed')).toBe('true')
    expect(names()).toEqual(['prod-1', 'db-01', 'nas'])
    first.unmount()
    render(HostsView)
    expect(screen.getByRole('button', { name: 'Список' }).getAttribute('aria-pressed')).toBe('true')
    await fireEvent.click(screen.getByRole('button', { name: 'Плитки' }))
  })

  it('duplicate opens the dialog with a copy that is not saved yet', async () => {
    load(list)
    render(HostsView)
    await fireEvent.click(screen.getAllByRole('button', { name: 'Ещё' })[0])
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Дублировать' }))
    const dialog = screen.getByRole('dialog', { name: 'Новый хост' })
    expect((within(dialog).getByLabelText('Название') as HTMLInputElement).value).toBe('prod-1 (копия)')
    expect((within(dialog).getByLabelText('Адрес') as HTMLInputElement).value).toBe('10.0.4.11')
    expect(api.HostSave).not.toHaveBeenCalled()
  })

  it('delete asks first and closes the host tabs', async () => {
    load(list)
    sess.sessions.list = [
      { id: 's1', hostId: 'h1', state: 'connected' },
      { id: 's2', hostId: 'h2', state: 'connected' },
    ]
    api.HostDelete.mockResolvedValue(undefined)
    render(HostsView)
    await fireEvent.click(screen.getAllByRole('button', { name: 'Ещё' })[0])
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Удалить' }))
    expect(api.HostDelete).not.toHaveBeenCalled()
    const dialog = screen.getByRole('dialog')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить' }))
    await vi.waitFor(() => expect(sess.closeSession).toHaveBeenCalledWith('s1'))
    expect(api.HostDelete).toHaveBeenCalledWith('h1')
    expect(sess.closeSession).not.toHaveBeenCalledWith('s2')
  })

  it('opens the host asked for by an error card', async () => {
    load(list)
    ui.editHostId = 'h2'
    render(HostsView)
    const dialog = await screen.findByRole('dialog', { name: 'Изменить хост' })
    expect((within(dialog).getByLabelText('Адрес') as HTMLInputElement).value).toBe('db.example.test')
    expect(ui.editHostId).toBe('')
  })

  it('the list view menu is anchored to its button and closes on Esc', async () => {
    load(list)
    render(HostsView)
    await fireEvent.click(screen.getByRole('button', { name: 'Список' }))
    const more = screen.getAllByRole('button', { name: 'Ещё' })[2]
    await fireEvent.click(more)
    const menu = screen.getByRole('menu')
    expect(within(menu).getAllByRole('menuitem').map((e) => e.textContent)).toEqual(['Изменить', 'Дублировать', 'Удалить'])
    expect(menu.style.top).not.toBe('')
    expect(document.activeElement).toBe(within(menu).getAllByRole('menuitem')[0])
    await fireEvent.keyDown(menu, { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(document.activeElement).toBe(more)
    await fireEvent.click(screen.getByRole('button', { name: 'Плитки' }))
  })

  it('Tab out of the menu closes it', async () => {
    load(list)
    render(HostsView)
    await fireEvent.click(screen.getAllByRole('button', { name: 'Ещё' })[0])
    const item = screen.getAllByRole('menuitem')[0]
    await fireEvent.focusOut(item, { relatedTarget: screen.getByLabelText('Поиск хостов по имени, адресу или пользователю') })
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('host tiles and rows open the native host menu on right-click', async () => {
    load(list)
    render(HostsView)
    expect(nativeMenu(screen.getAllByTestId('host-name')[1])).toEqual(['host', 'h2'])
    await fireEvent.click(screen.getByRole('button', { name: 'Список' }))
    expect(nativeMenu(screen.getAllByTestId('host-name')[0])).toEqual(['host', 'h1'])
    await fireEvent.click(screen.getByRole('button', { name: 'Плитки' }))
  })

  it('native menu items edit, duplicate and delete the host they were opened on', async () => {
    load(list)
    render(HostsView)
    await menuAction('host', 'edit', 'h2')
    let dialog = screen.getByRole('dialog', { name: 'Изменить хост' })
    expect((within(dialog).getByLabelText('Адрес') as HTMLInputElement).value).toBe('db.example.test')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }))
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await menuAction('host', 'duplicate', 'h1')
    dialog = screen.getByRole('dialog', { name: 'Новый хост' })
    expect((within(dialog).getByLabelText('Название') as HTMLInputElement).value).toBe('prod-1 (копия)')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }))
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await menuAction('host', 'delete', 'h3')
    expect(screen.getByRole('dialog', { name: 'Удалить хост nas?' })).toBeTruthy()
    expect(api.HostDelete).not.toHaveBeenCalled()
  })

  it('native menu actions of other menus or unknown hosts are ignored', async () => {
    load(list)
    render(HostsView)
    await menuAction('key', 'delete', 'h1')
    await menuAction('host', 'delete', 'gone')
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('a failed delete keeps the host tabs', async () => {
    load(list)
    sess.sessions.list = [{ id: 's1', hostId: 'h1', state: 'connected' }]
    api.HostDelete.mockRejectedValue('disk full')
    render(HostsView)
    await fireEvent.click(screen.getAllByRole('button', { name: 'Ещё' })[0])
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Удалить' }))
    await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Удалить' }))
    await vi.waitFor(() => expect(api.HostDelete).toHaveBeenCalledWith('h1'))
    await Promise.resolve()
    expect(sess.closeSession).not.toHaveBeenCalled()
  })

  describe('groups', () => {
    const group = { id: 'g1', name: 'Production', host_count: 1, connected_count: 0 }
    beforeEach(() => {
      load([{ ...list[0], group_id: 'g1', group_name: 'Production' }, list[1]])
      groups.splice(0, groups.length, group as never)
      api.GroupsList.mockResolvedValue([group])
    })

    it('creates a group and shows the Go error inline', async () => {
      api.GroupSave.mockRejectedValueOnce('field:name: Группа с таким названием уже есть').mockResolvedValueOnce({ id: 'g2', name: 'Lab' })
      render(HostsView)
      await fireEvent.click(screen.getByRole('button', { name: 'Новая группа' }))
      const dialog = screen.getByRole('dialog', { name: 'Новая группа' })
      await fireEvent.input(within(dialog).getByLabelText('Название группы'), { target: { value: 'Lab' } })
      await fireEvent.click(within(dialog).getByRole('button', { name: 'Создать' }))
      expect((await within(dialog).findByRole('alert')).textContent).toBe('Группа с таким названием уже есть')
      await fireEvent.click(within(dialog).getByRole('button', { name: 'Создать' }))
      await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      expect(api.GroupSave).toHaveBeenLastCalledWith('Lab')
    })

    it('renames a group from its menu', async () => {
      api.GroupRename.mockResolvedValue(undefined)
      render(HostsView)
      await fireEvent.click(screen.getByRole('button', { name: 'Ещё о группе Production' }))
      await fireEvent.click(screen.getByRole('menuitem', { name: 'Переименовать' }))
      const dialog = screen.getByRole('dialog', { name: 'Переименовать группу' })
      const input = within(dialog).getByLabelText('Название группы') as HTMLInputElement
      expect(input.value).toBe('Production')
      await fireEvent.input(input, { target: { value: 'Prod' } })
      await fireEvent.click(within(dialog).getByRole('button', { name: 'Переименовать' }))
      await vi.waitFor(() => expect(api.GroupRename).toHaveBeenCalledWith('g1', 'Prod'))
    })

    it('deletes a group after confirmation', async () => {
      api.GroupDelete.mockResolvedValue(undefined)
      render(HostsView)
      await fireEvent.click(screen.getByRole('button', { name: 'Ещё о группе Production' }))
      await fireEvent.click(screen.getByRole('menuitem', { name: 'Удалить' }))
      expect(api.GroupDelete).not.toHaveBeenCalled()
      await fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Удалить' }))
      await vi.waitFor(() => expect(api.GroupDelete).toHaveBeenCalledWith('g1'))
    })

    it('group tiles open the native group menu; its items rename and delete', async () => {
      render(HostsView)
      expect(nativeMenu(screen.getByRole('button', { name: /^Production/ }))).toEqual(['group', 'g1'])
      await menuAction('group', 'rename', 'g1')
      const dialog = screen.getByRole('dialog', { name: 'Переименовать группу' })
      expect((within(dialog).getByLabelText('Название группы') as HTMLInputElement).value).toBe('Production')
      await fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }))
      await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      await menuAction('group', 'delete', 'g1')
      expect(screen.getByRole('dialog', { name: 'Удалить группу Production?' })).toBeTruthy()
      expect(api.GroupDelete).not.toHaveBeenCalled()
    })

    it('a group tile filters the hosts', async () => {
      render(HostsView)
      const tile = screen.getByRole('button', { name: /^Production/ })
      await fireEvent.click(tile)
      expect(tile.getAttribute('aria-pressed')).toBe('true')
      expect(names()).toEqual(['prod-1'])
      await fireEvent.click(tile)
      expect(names()).toEqual(['prod-1', 'db-01'])
    })
  })
})
