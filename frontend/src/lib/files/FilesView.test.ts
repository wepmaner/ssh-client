import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { handlers, api } = vi.hoisted(() => ({
  handlers: new Map<string, (p: unknown) => void>(),
  api: {
    FilesOpen: vi.fn(),
    FilesClose: vi.fn(),
    FilesHome: vi.fn(),
    FilesList: vi.fn(),
    FilesLoginAttach: vi.fn(),
    FilesLoginWrite: vi.fn(),
    FilesRetry: vi.fn(),
    FilesMkdir: vi.fn(),
    FilesRename: vi.fn(),
    FilesMove: vi.fn(),
    FilesDelete: vi.fn(),
    FilesConflicts: vi.fn(),
    FilesCopy: vi.fn(),
    FilesUpload: vi.fn(),
    FilesDownload: vi.fn(),
    FilesPickUpload: vi.fn(),
    FilesPickDownloadDir: vi.fn(),
    FilesJobCancel: vi.fn(),
    FilesJobRetry: vi.fn(),
    FilesJobRemove: vi.fn(),
    FilesShowLocal: vi.fn(),
    SessionOpen: vi.fn(),
    SessionClose: vi.fn(),
    SessionList: vi.fn(),
    ClipboardSetText: vi.fn(),
    ClipboardGetText: vi.fn(),
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
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 6
    open(el: HTMLElement) {
      el.setAttribute('data-testid', 'xterm')
    }
    loadAddon() {}
    focus() {}
    dispose() {}
    resize() {}
    attachCustomKeyEventHandler() {}
    onData() {
      return { dispose() {} }
    }
    write() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    proposeDimensions() {
      return undefined
    }
  },
}))

import { closeSession, openFiles, requestClose, sessions } from '../sessions.svelte'
import { hosts } from '../stores.svelte'
import CloseFilesDialog from './CloseFilesDialog.svelte'
import { filesClipboard, filesTab, resetFilesMemory } from './files.svelte'
import FilesView from './FilesView.svelte'

const entry = (name: string, dir = false) => ({ name, dir, link: false, broken: false, size: 2048, mtime: 0, mode: dir ? 'drwxr-xr-x' : '-rw-r--r--' })
let listing: Record<string, ReturnType<typeof entry>[]>

const flush = async () => {
  for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0))
  await tick()
}

async function setup() {
  await openFiles('h1')
  await flush()
  const tab = sessions.list[0]
  render(FilesView, { tab, visible: true, onopenhost: () => {} })
  render(CloseFilesDialog)
  await flush()
  return tab
}

const names = () => screen.queryAllByTestId('file-name').map((e) => e.textContent)
/** The names of the open menu's items, without their shortcut hints. */
const menuLabels = () =>
  within(screen.getByRole('menu'))
    .getAllByRole('menuitem')
    .map((e) => e.querySelector('.label')?.textContent)
const menuHints = () =>
  Object.fromEntries(
    within(screen.getByRole('menu'))
      .getAllByRole('menuitem')
      .map((e) => [e.querySelector('.label')?.textContent, e.querySelector('.hint')?.textContent ?? '']),
  )
const row = (name: string) => screen.getAllByRole('option').find((o) => o.textContent?.includes(name))!

beforeEach(() => {
  for (const t of [...sessions.list]) closeSession(t.id)
  vi.resetAllMocks()
  handlers.clear()
  resetFilesMemory()
  hosts.splice(0, hosts.length, { id: 'h1', name: 'prod', address: 'p', port: 22, user: 'u', group_id: '', group_name: '', key_id: '', key_name: '', os: '', mcp_access: '', connected: false })
  listing = { '/home/u': [entry('docs', true), entry('a.txt'), entry('b.log')], '/home/u/docs': [] }
  api.FilesOpen.mockResolvedValue('f1')
  api.FilesClose.mockResolvedValue(undefined)
  api.FilesHome.mockResolvedValue('/home/u')
  api.FilesLoginAttach.mockResolvedValue(undefined)
  api.FilesList.mockImplementation((_id: string, dir: string) => Promise.resolve({ path: dir, entries: listing[dir] ?? [] }))
})

describe('FilesView', () => {
  it('lists the home folder, folders first, with the footer', async () => {
    const tab = await setup()
    expect(tab.type).toBe('files')
    expect(tab.state).toBe('connected')
    expect(names()).toEqual(['docs', 'a.txt', 'b.log'])
    expect(screen.getByText('3 объекта')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Назад' }).hasAttribute('disabled')).toBe(true)
    await fireEvent.click(row('a.txt'))
    expect(screen.getByText('3 объекта · выбрано 1 · 2 КБ')).toBeTruthy()
  })

  it('opens a folder on double click and shows the empty state there', async () => {
    await setup()
    await fireEvent.dblClick(row('docs'))
    await flush()
    expect(api.FilesList).toHaveBeenLastCalledWith('f1', '/home/u/docs')
    expect(screen.getByText('Папка пуста. Перетащите сюда файлы с компьютера')).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Назад' }))
    await flush()
    expect(names()).toContain('a.txt')
  })

  it('marks the list and folder rows as drop targets of their tab and folder', async () => {
    await setup()
    const list = screen.getByRole('listbox')
    expect(list.getAttribute('data-file-drop-target')).toBe('')
    expect(list.getAttribute('data-files-tab')).toBe('f1')
    expect(list.getAttribute('data-files-dir')).toBe('/home/u')
    expect(row('docs').getAttribute('data-files-dir')).toBe('/home/u/docs')
    expect(row('a.txt').hasAttribute('data-file-drop-target')).toBe(false)
  })

  it('has the item menu and the folder menu', async () => {
    await setup()
    await fireEvent.contextMenu(row('a.txt'), { clientX: 50, clientY: 60 })
    expect(menuLabels()).toEqual(['Скачать…', 'Вырезать', 'Копировать', 'Переименовать', 'Копировать путь', 'Удалить'])
    expect(menuHints()).toEqual({ 'Скачать…': 'Enter', Вырезать: 'Ctrl+X', Копировать: 'Ctrl+C', Переименовать: 'F2', 'Копировать путь': 'Ctrl+Shift+C', Удалить: 'Del' })
    await fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })

    await fireEvent.contextMenu(row('docs'), { clientX: 50, clientY: 60 })
    expect(menuLabels()[0]).toBe('Открыть')
    expect(menuHints()['Открыть']).toBe('Enter')
    expect(menuHints()['Скачать…']).toBe('')
    await fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })

    await fireEvent.contextMenu(screen.getByRole('listbox'), { clientX: 50, clientY: 300 })
    expect(menuLabels()).toEqual(['Загрузить файлы…', 'Загрузить папку…', 'Новая папка', 'Выделить всё', 'Обновить', 'Копировать путь папки', 'Показать скрытые'])
    expect(menuHints()).toMatchObject({ 'Новая папка': 'Ctrl+Shift+N', 'Выделить всё': 'Ctrl+A', Обновить: 'F5' })
  })

  it('renames in the row with F2 and Enter', async () => {
    await setup()
    await fireEvent.click(row('a.txt'))
    await fireEvent.keyDown(row('a.txt'), { key: 'F2' })
    const input = screen.getByLabelText('Новое имя') as HTMLInputElement
    expect(input.value).toBe('a.txt')
    expect([input.selectionStart, input.selectionEnd]).toEqual([0, 1])
    api.FilesRename.mockResolvedValue('c.txt')
    listing['/home/u'] = [entry('docs', true), entry('c.txt'), entry('b.log')]
    await fireEvent.input(input, { target: { value: 'c.txt' } })
    await fireEvent.keyDown(input, { key: 'Enter' })
    await flush()
    expect(api.FilesRename).toHaveBeenCalledWith('f1', '/home/u/a.txt', 'c.txt')
    expect(names()).toEqual(['docs', 'b.log', 'c.txt'])
    expect(screen.queryByLabelText('Новое имя')).toBeNull()
  })

  it('shows a refused name next to the field', async () => {
    await setup()
    await fireEvent.click(row('a.txt'))
    await fireEvent.keyDown(row('a.txt'), { key: 'F2' })
    const input = screen.getByLabelText('Новое имя')
    api.FilesRename.mockRejectedValue('Имя «b.log» уже занято')
    await fireEvent.input(input, { target: { value: 'b.log' } })
    await fireEvent.keyDown(input, { key: 'Enter' })
    await flush()
    expect(screen.getByRole('alert').textContent).toBe('Имя «b.log» уже занято')
    await fireEvent.keyDown(screen.getByLabelText('Новое имя'), { key: 'Escape' })
    expect(screen.queryByLabelText('Новое имя')).toBeNull()
  })

  it('asks before Del and deletes through FilesDelete', async () => {
    await setup()
    await fireEvent.click(row('a.txt'))
    await fireEvent.click(row('b.log'), { ctrlKey: true })
    await fireEvent.keyDown(row('b.log'), { key: 'Delete' })
    const dialog = screen.getByRole('dialog', { name: 'Удалить 2 объекта?' })
    expect(dialog.textContent).toContain('Это нельзя отменить')
    expect(api.FilesDelete).not.toHaveBeenCalled()
    // Cancel has the focus, the red button does not: Enter never deletes by itself.
    const del = within(dialog).getByRole('button', { name: 'Удалить' })
    expect(del.classList.contains('danger')).toBe(true)
    expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Отмена' }))
    api.FilesDelete.mockResolvedValue('j1')
    await fireEvent.click(del)
    await flush()
    expect(api.FilesDelete).toHaveBeenCalledWith('f1', ['/home/u/a.txt', '/home/u/b.log'])
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('cuts, copies and pastes with Ctrl+X, Ctrl+C and Ctrl+V; the menu pastes into a folder', async () => {
    await setup()
    await fireEvent.click(row('a.txt'))
    await fireEvent.keyDown(row('a.txt'), { key: 'c', code: 'KeyC', ctrlKey: true })
    expect(filesClipboard.value).toEqual({ hostId: 'h1', paths: ['/home/u/a.txt'], cut: false })
    api.FilesConflicts.mockResolvedValue([])
    api.FilesCopy.mockResolvedValue('j1')
    await fireEvent.keyDown(row('a.txt'), { key: 'v', code: 'KeyV', ctrlKey: true })
    await flush()
    expect(api.FilesCopy).toHaveBeenCalledWith('f1', ['/home/u/a.txt'], '/home/u', 'skip')

    await fireEvent.contextMenu(row('docs'), { clientX: 50, clientY: 60 })
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Вставить в папку' }))
    await flush()
    expect(api.FilesCopy).toHaveBeenLastCalledWith('f1', ['/home/u/a.txt'], '/home/u/docs', 'skip')

    await fireEvent.click(row('b.log'))
    await fireEvent.keyDown(row('b.log'), { key: 'x', code: 'KeyX', ctrlKey: true })
    expect(row('b.log').classList.contains('cut')).toBe(true)
    await fireEvent.contextMenu(screen.getByRole('listbox'), { clientX: 50, clientY: 300 })
    expect(menuLabels()[0]).toBe('Вставить')
    expect(menuHints()['Вставить']).toBe('Ctrl+V')
    await fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    api.FilesMove.mockResolvedValue(undefined)
    await fireEvent.contextMenu(row('docs'), { clientX: 50, clientY: 60 })
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Вставить в папку' }))
    await flush()
    expect(api.FilesMove).toHaveBeenCalledWith('f1', ['/home/u/b.log'], '/home/u/docs')
    expect(filesClipboard.value).toBeNull()
  })

  it('goes back and forward with the side buttons of the mouse', async () => {
    await setup()
    await fireEvent.dblClick(row('docs'))
    await flush()
    expect(api.FilesList).toHaveBeenLastCalledWith('f1', '/home/u/docs')
    const back = new MouseEvent('mouseup', { button: 3, bubbles: true, cancelable: true })
    screen.getByRole('listbox').dispatchEvent(back)
    await flush()
    expect(back.defaultPrevented).toBe(true)
    expect(api.FilesList).toHaveBeenLastCalledWith('f1', '/home/u')
    screen.getByRole('listbox').dispatchEvent(new MouseEvent('mouseup', { button: 4, bubbles: true, cancelable: true }))
    await flush()
    expect(api.FilesList).toHaveBeenLastCalledWith('f1', '/home/u/docs')
  })

  it('downloads a file on double click and on Enter, after the folder picker', async () => {
    await setup()
    api.FilesPickDownloadDir.mockResolvedValue('C:\\Загрузки')
    api.FilesConflicts.mockResolvedValue([])
    api.FilesDownload.mockResolvedValue('j1')
    await fireEvent.dblClick(row('a.txt'))
    await flush()
    expect(api.FilesPickDownloadDir).toHaveBeenCalledTimes(1)
    expect(api.FilesDownload).toHaveBeenCalledWith('f1', ['/home/u/a.txt'], 'C:\\Загрузки', 'skip')
    await fireEvent.click(row('b.log'))
    await fireEvent.keyDown(row('b.log'), { key: 'Enter' })
    await flush()
    expect(api.FilesDownload).toHaveBeenLastCalledWith('f1', ['/home/u/b.log'], 'C:\\Загрузки', 'skip')
  })

  it('shows the hidden toggle pressed, and how many names it hides', async () => {
    listing['/home/u'].push(entry('.env'), entry('.cache', true))
    await setup()
    const toggle = screen.getByRole('button', { name: 'Скрытые' })
    expect(toggle.getAttribute('aria-pressed')).toBe('false')
    expect(screen.getByText(/скрыто: 2/)).toBeTruthy()
    await fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-pressed')).toBe('true')
    expect(screen.queryByText(/скрыто:/)).toBeNull()
    expect(names()).toContain('.env')
  })

  it('creates a folder in a new row', async () => {
    await setup()
    await fireEvent.click(screen.getByRole('button', { name: 'Новая папка' }))
    const input = screen.getByLabelText('Имя новой папки') as HTMLInputElement
    expect(input.value).toBe('Новая папка')
    api.FilesMkdir.mockResolvedValue('/home/u/src')
    listing['/home/u'] = [...listing['/home/u'], entry('src', true)]
    await fireEvent.input(input, { target: { value: 'src' } })
    await fireEvent.keyDown(input, { key: 'Enter' })
    await flush()
    expect(api.FilesMkdir).toHaveBeenCalledWith('f1', '/home/u', 'src')
    expect(row('src').getAttribute('aria-selected')).toBe('true')
  })

  it('asks about clashes before an upload and passes the answer', async () => {
    await setup()
    api.FilesPickUpload.mockResolvedValue(['C:\\a.txt', 'C:\\new.txt'])
    api.FilesConflicts.mockResolvedValue([{ name: 'a.txt', merge: false }])
    api.FilesUpload.mockResolvedValue('j1')
    await fireEvent.click(screen.getByRole('button', { name: /Загрузить/ }))
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Файлы…' }))
    await flush()
    let dialog = screen.getByRole('dialog', { name: 'Такие имена уже есть' })
    expect(dialog.textContent).toContain('В папке уже есть: a.txt')
    expect(dialog.textContent).not.toContain('сливаются') // no folder among the clashes
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Заменить' }))
    await flush()
    expect(api.FilesUpload).toHaveBeenCalledWith('f1', ['C:\\a.txt', 'C:\\new.txt'], '/home/u', 'replace')

    api.FilesConflicts.mockResolvedValue([{ name: 'a.txt', merge: false }, { name: 'docs', merge: true }])
    await fireEvent.click(screen.getByRole('button', { name: /Загрузить/ }))
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Файлы…' }))
    await flush()
    dialog = screen.getByRole('dialog', { name: 'Такие имена уже есть' })
    expect(dialog.textContent).toContain('Папки с тем же именем сливаются')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }))
  })

  it('shows the queue with the job state and the tab percent', async () => {
    const tab = await setup()
    handlers.get('files:job:f1')!({ id: 'j1', kind: 'download', title: 'a.txt', state: 'running', message: '', detail: '', total_bytes: 100, done_bytes: 45, total_files: 1, done_files: 0, failed: 0, renamed: 0, current: 'a.txt', speed_bps: 0, dest: 'C:\\Загрузки', retry_in_s: 0 })
    await tick()
    const queue = screen.getByRole('region', { name: 'Передачи' })
    expect(queue.textContent).toContain('Передаётся 1 из 1')
    expect(filesTab(tab.id)?.progress).toBe(45)
    api.FilesJobCancel.mockResolvedValue(undefined)
    expect(within(queue).queryByRole('button', { name: 'Убрать из списка' })).toBeNull()
    await fireEvent.click(within(queue).getByRole('button', { name: 'Отменить' }))
    expect(api.FilesJobCancel).toHaveBeenCalledWith('f1', 'j1')
    handlers.get('files:job:f1')!({ id: 'j1', kind: 'download', title: 'a.txt', state: 'done', message: '', detail: '', total_bytes: 100, done_bytes: 100, total_files: 1, done_files: 1, failed: 0, renamed: 0, skipped: 1, current: '', speed_bps: 0, dest: 'C:\\Загрузки', retry_in_s: 0 })
    await tick()
    // Finished: no summary text, no bar, the result on the row and its own × that removes it.
    expect(queue.textContent).not.toContain('завершены')
    expect(queue.textContent).toContain('Передачи')
    expect(queue.textContent).toContain('100 байт · пропущено: 1')
    expect(within(queue).queryByRole('progressbar')).toBeNull()
    expect(within(queue).queryByRole('button', { name: 'Отменить' })).toBeNull()
    await fireEvent.click(within(queue).getByRole('button', { name: 'Показать в папке' }))
    expect(api.FilesShowLocal).toHaveBeenCalledWith('C:\\Загрузки')
    api.FilesJobRemove.mockResolvedValue(undefined)
    await fireEvent.click(within(queue).getByRole('button', { name: 'Убрать из списка' }))
    expect(api.FilesJobRemove).toHaveBeenCalledWith('f1', 'j1')
  })

  it('offers «Повторить сейчас» on a waiting job and lists active jobs first', async () => {
    await setup()
    const base = { kind: 'upload', message: '', detail: '', total_bytes: 10, done_bytes: 1, total_files: 1, done_files: 0, failed: 0, renamed: 0, skipped: 0, current: '', speed_bps: 0, dest: '', retry_in_s: 0 }
    handlers.get('files:job:f1')!({ ...base, id: 'old', title: 'old.txt', state: 'done', done_bytes: 10, done_files: 1 })
    handlers.get('files:job:f1')!({ ...base, id: 'w', title: 'w.txt', state: 'waiting', retry_in_s: 30 })
    await tick()
    const queue = screen.getByRole('region', { name: 'Передачи' })
    const titles = within(queue).getAllByRole('listitem').map((li) => li.querySelector('.title')?.textContent?.trim())
    expect(titles).toEqual(['w.txt', 'old.txt'])
    api.FilesJobRetry.mockResolvedValue(undefined)
    await fireEvent.click(within(queue).getByRole('button', { name: 'Повторить сейчас' }))
    expect(api.FilesJobRetry).toHaveBeenCalledWith('f1', 'w')
  })

  it('goes up with Backspace when the folder failed to open', async () => {
    const tab = await setup()
    const m = filesTab(tab.id)!
    m.listError = 'Нет доступа'
    await tick()
    ;(document.activeElement as HTMLElement | null)?.blur()
    const up = vi.spyOn(m, 'up').mockResolvedValue(true)
    await fireEvent.keyDown(window, { key: 'Backspace' })
    expect(up).toHaveBeenCalled()
  })

  it('asks before closing a tab with a running transfer', async () => {
    const tab = await setup()
    handlers.get('files:job:f1')!({ id: 'j1', kind: 'upload', title: 'x', state: 'running', message: '', detail: '', total_bytes: 10, done_bytes: 1, total_files: 1, done_files: 0, failed: 0, renamed: 0, current: '', speed_bps: 0, dest: '', retry_in_s: 0 })
    requestClose(tab.id)
    await tick()
    const dialog = screen.getByRole('dialog', { name: 'Остановить передачи и закрыть?' })
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }))
    expect(sessions.list).toHaveLength(1)
    expect(api.FilesClose).not.toHaveBeenCalled()
    requestClose(tab.id)
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Остановить и закрыть' }))
    expect(api.FilesClose).toHaveBeenCalledWith('f1')
    expect(sessions.list).toHaveLength(0)
  })

  it('closes an idle tab without a question; a second open activates the same tab', async () => {
    const tab = await setup()
    await openFiles('h1')
    expect(api.FilesOpen).toHaveBeenCalledTimes(1)
    expect(sessions.activeId).toBe(tab.id)
    requestClose(tab.id)
    expect(api.FilesClose).toHaveBeenCalledWith('f1')
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('shows the login card while the first connect goes on', async () => {
    api.FilesHome.mockRejectedValue('нет подключения')
    await openFiles('h1')
    await flush()
    const tab = sessions.list[0]
    expect(tab.state).toBe('connecting')
    render(FilesView, { tab, visible: true, onopenhost: () => {} })
    handlers.get('files:login:f1')!(btoa('Password: '))
    await flush()
    expect(screen.getByRole('heading', { name: 'Вход на prod' })).toBeTruthy()
    expect(screen.getByTestId('xterm')).toBeTruthy()
    api.FilesHome.mockResolvedValue('/home/u')
    handlers.get('files:state:f1')!({ state: 'connected', retryable: true })
    await flush()
    expect(screen.queryByRole('heading', { name: 'Вход на prod' })).toBeNull()
    expect(names()).toEqual(['docs', 'a.txt', 'b.log'])
  })

  it('Ctrl+L turns the path into a field; Enter goes there', async () => {
    await setup()
    await fireEvent.keyDown(window, { key: 'l', code: 'KeyL', ctrlKey: true })
    await tick()
    const field = screen.getByLabelText('Путь на сервере') as HTMLInputElement
    expect(field.value).toBe('/home/u')
    await fireEvent.input(field, { target: { value: '/home/u/docs' } })
    await fireEvent.keyDown(field, { key: 'Enter' })
    await flush()
    expect(api.FilesList).toHaveBeenLastCalledWith('f1', '/home/u/docs')
    expect(screen.getByRole('button', { name: 'docs' }).getAttribute('aria-current')).toBe('location')
  })

  it('drags selected rows onto a folder to move them, not as an Explorer drop', async () => {
    await setup()
    await fireEvent.click(row('a.txt'))
    await fireEvent.click(row('b.log'), { ctrlKey: true })
    const types: string[] = []
    const dataTransfer = {
      types,
      setData: (t: string) => types.push(t),
      effectAllowed: '',
      dropEffect: '',
    }
    await fireEvent.dragStart(row('b.log'), { dataTransfer })
    expect(types).toEqual(['application/x-sshclient-files'])
    await fireEvent.dragOver(row('docs'), { dataTransfer })
    expect(row('docs').classList.contains('over')).toBe(true)
    api.FilesMove.mockResolvedValue(undefined)
    await fireEvent.drop(row('docs'), { dataTransfer })
    await flush()
    expect(api.FilesMove).toHaveBeenCalledWith('f1', ['/home/u/a.txt', '/home/u/b.log'], '/home/u/docs')
    expect(api.FilesUpload).not.toHaveBeenCalled()
  })

  it('ignores a drag of Explorer files: Go handles it through files:drop', async () => {
    await setup()
    const dataTransfer = { types: ['Files'], dropEffect: '' }
    await fireEvent.dragOver(row('docs'), { dataTransfer })
    expect(row('docs').classList.contains('over')).toBe(false)
    await fireEvent.drop(row('docs'), { dataTransfer })
    expect(api.FilesMove).not.toHaveBeenCalled()
  })
})
