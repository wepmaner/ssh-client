import { beforeEach, describe, expect, it, vi } from 'vitest'

const { handlers, api } = vi.hoisted(() => ({
  handlers: new Map<string, (p: unknown) => void>(),
  api: {
    FilesHome: vi.fn(),
    FilesList: vi.fn(),
    FilesLoginAttach: vi.fn(),
    FilesRetry: vi.fn(),
    FilesMkdir: vi.fn(),
    FilesRename: vi.fn(),
    FilesMove: vi.fn(),
    FilesDelete: vi.fn(),
    FilesConflicts: vi.fn(),
    FilesUpload: vi.fn(),
    FilesDownload: vi.fn(),
    FilesPickUpload: vi.fn(),
    FilesPickDownloadDir: vi.fn(),
    FilesJobCancel: vi.fn(),
    FilesJobRetry: vi.fn(),
    FilesJobRemove: vi.fn(),
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

import { toastState } from '../stores.svelte'
import { createFilesTab, disposeFilesTab, filesTab, type FilesTab, type Job } from './files.svelte'
import type { Entry } from './format'

const e = (name: string, over: Partial<Entry> = {}): Entry => ({ name, dir: false, link: false, broken: false, size: 10, mtime: 0, mode: '-rw-r--r--', ...over })

/** A remote tree: path → entries. */
let tree: Record<string, Entry[]>

function listOf(dir: string) {
  if (!(dir in tree)) return Promise.reject('Папка не найдена')
  return Promise.resolve({ path: dir, entries: tree[dir] })
}

const flush = () => new Promise((r) => setTimeout(r, 0))

async function open(id = 't1'): Promise<FilesTab> {
  const t = createFilesTab(id, 'h1')
  await flush()
  await flush()
  return t
}

beforeEach(() => {
  vi.resetAllMocks()
  for (const id of ['t1', 't2']) disposeFilesTab(id)
  handlers.clear()
  tree = {
    '/home/u': [e('docs', { dir: true }), e('b.txt'), e('.profile'), e('a.txt')],
    '/home/u/docs': [e('x.md')],
    '/home': [e('u', { dir: true })],
    '/': [e('home', { dir: true })],
  }
  api.FilesHome.mockResolvedValue('/home/u')
  api.FilesList.mockImplementation((_id: string, dir: string) => listOf(dir))
  api.FilesLoginAttach.mockResolvedValue(undefined)
})

describe('FilesTab navigation', () => {
  it('starts in the home folder with hidden names hidden and folders first', async () => {
    const t = await open()
    expect(api.FilesLoginAttach).toHaveBeenCalledWith('t1')
    expect(t.path).toBe('/home/u')
    expect(t.order).toEqual(['docs', 'a.txt', 'b.txt'])
    t.toggleHidden()
    expect(t.order).toEqual(['docs', '.profile', 'a.txt', 'b.txt'])
  })

  it('keeps a back and forward history; a new folder drops the forward part', async () => {
    const t = await open()
    await t.navigate('/home/u/docs')
    expect(t.canBack).toBe(true)
    await t.back()
    expect(t.path).toBe('/home/u')
    expect(t.canForward).toBe(true)
    await t.forward()
    expect(t.path).toBe('/home/u/docs')
    await t.back()
    await t.navigate('/home')
    expect(t.canForward).toBe(false)
    expect(t.history).toEqual(['/home/u', '/home'])
  })

  it('goes up and selects the folder it came from', async () => {
    const t = await open()
    await t.up()
    expect(t.path).toBe('/home')
    expect(t.sel.selected).toEqual(['u'])
    await t.up()
    expect(t.path).toBe('/')
    expect(t.canUp).toBe(false)
  })

  it('keeps the path of a folder it cannot read, with the reason', async () => {
    const t = await open()
    await t.navigate('/nope')
    expect(t.path).toBe('/nope')
    expect(t.listError).toBe('Папка не найдена')
    expect(t.entries).toEqual([])
    await t.back()
    expect(t.listError).toBe('')
    expect(t.path).toBe('/home/u')
  })

  it('sorts by a column, again descending', async () => {
    const t = await open()
    t.setSort('name')
    expect(t.order).toEqual(['docs', 'b.txt', 'a.txt'])
    t.setSort('size')
    expect(t.sort).toEqual({ key: 'size', desc: false })
  })

  it('filters by name at once', async () => {
    const t = await open()
    t.filter = 'B.'
    expect(t.order).toEqual(['b.txt'])
  })

  it('refreshes on files:changed of the current folder only', async () => {
    const t = await open()
    api.FilesList.mockClear()
    handlers.get('files:changed:t1')!({ dir: '/elsewhere' })
    expect(api.FilesList).not.toHaveBeenCalled()
    tree['/home/u'].push(e('new.bin'))
    handlers.get('files:changed:t1')!({ dir: '/home/u/' })
    await flush()
    expect(api.FilesList).toHaveBeenCalledWith('t1', '/home/u')
    expect(t.order).toContain('new.bin')
  })

  it('a probe that fails waits for the connected state', async () => {
    api.FilesHome.mockRejectedValueOnce('нет подключения')
    const t = await open()
    expect(t.loaded).toBe(false)
    expect(t.listError).toBe('')
    t.onState('connected')
    await flush()
    await flush()
    expect(t.path).toBe('/home/u')
  })
})

describe('FilesTab changes', () => {
  it('renames in place and selects the new name; an error stays by the field', async () => {
    const t = await open()
    t.startRename('a.txt')
    api.FilesRename.mockRejectedValueOnce('Имя уже занято')
    expect(await t.rename('a.txt', 'b.txt')).toBe(false)
    expect(t.renameError).toBe('Имя уже занято')
    expect(t.renaming).toBe('a.txt')
    api.FilesRename.mockResolvedValueOnce('c.txt')
    tree['/home/u'] = tree['/home/u'].map((x) => (x.name === 'a.txt' ? e('c.txt') : x))
    expect(await t.rename('a.txt', 'c.txt')).toBe(true)
    expect(api.FilesRename).toHaveBeenLastCalledWith('t1', '/home/u/a.txt', 'c.txt')
    expect(t.renaming).toBe('')
    expect(t.sel.selected).toEqual(['c.txt'])
  })

  it('an unchanged name closes the editor without a call', async () => {
    const t = await open()
    t.startRename('a.txt')
    expect(await t.rename('a.txt', ' a.txt ')).toBe(true)
    expect(api.FilesRename).not.toHaveBeenCalled()
  })

  it('creates a folder and selects it', async () => {
    const t = await open()
    t.startCreate()
    api.FilesMkdir.mockResolvedValue('/home/u/Новая папка')
    tree['/home/u'].push(e('Новая папка', { dir: true }))
    expect(await t.mkdir('Новая папка')).toBe(true)
    expect(api.FilesMkdir).toHaveBeenCalledWith('t1', '/home/u', 'Новая папка')
    expect(t.creating).toBe(false)
    expect(t.sel.selected).toEqual(['Новая папка'])
  })

  it('moves rows into a folder, never into itself or where they already are', async () => {
    const t = await open()
    await t.move(['/home/u/docs'], '/home/u/docs')
    await t.move(['/home/u/a.txt'], '/home/u')
    expect(api.FilesMove).not.toHaveBeenCalled()
    await t.move(['/home/u/a.txt', '/home/u/docs'], '/home/u/docs')
    expect(api.FilesMove).toHaveBeenCalledWith('t1', ['/home/u/a.txt'], '/home/u/docs')
  })

  it('deletes after the confirmation only', async () => {
    const t = await open()
    t.askDelete(['a.txt', 'docs'])
    expect(api.FilesDelete).not.toHaveBeenCalled()
    api.FilesDelete.mockResolvedValue('j1')
    await t.confirmDelete()
    expect(api.FilesDelete).toHaveBeenCalledWith('t1', ['/home/u/a.txt', '/home/u/docs'])
    expect(t.deleteAsk).toBeNull()
  })
})

describe('FilesTab transfers', () => {
  it('uploads without a question when nothing clashes', async () => {
    const t = await open()
    api.FilesPickUpload.mockResolvedValue(['C:\\a.txt'])
    api.FilesConflicts.mockResolvedValue([])
    api.FilesUpload.mockResolvedValue('j1')
    expect(await t.upload(false)).toBe('j1')
    expect(api.FilesPickUpload).toHaveBeenCalledWith(false)
    expect(api.FilesConflicts).toHaveBeenCalledWith('t1', 'upload', ['C:\\a.txt'], '/home/u')
    expect(api.FilesUpload).toHaveBeenCalledWith('t1', ['C:\\a.txt'], '/home/u', 'skip')
  })

  it('a cancelled picker queues nothing', async () => {
    const t = await open()
    api.FilesPickUpload.mockResolvedValue([])
    api.FilesPickDownloadDir.mockResolvedValue('')
    t.sel = { selected: ['a.txt'], anchor: 'a.txt', cursor: 'a.txt' }
    expect(await t.upload(true)).toBe('')
    expect(await t.download()).toBe('')
    expect(api.FilesConflicts).not.toHaveBeenCalled()
  })

  it('asks about clashes: Заменить, Пропустить and Отмена', async () => {
    const t = await open()
    t.sel = { selected: ['a.txt', 'docs'], anchor: 'a.txt', cursor: 'docs' }
    api.FilesPickDownloadDir.mockResolvedValue('C:\\Загрузки')
    api.FilesConflicts.mockResolvedValue(['a.txt'])
    api.FilesDownload.mockResolvedValue('j2')

    let p = t.download()
    await flush()
    expect(t.conflict?.names).toEqual(['a.txt'])
    t.conflict!.resolve('replace')
    expect(await p).toBe('j2')
    expect(api.FilesConflicts).toHaveBeenCalledWith('t1', 'download', ['/home/u/a.txt', '/home/u/docs'], 'C:\\Загрузки')
    expect(api.FilesDownload).toHaveBeenLastCalledWith('t1', ['/home/u/a.txt', '/home/u/docs'], 'C:\\Загрузки', 'replace')

    p = t.download()
    await flush()
    t.conflict!.resolve('skip')
    await p
    expect(api.FilesDownload).toHaveBeenLastCalledWith('t1', ['/home/u/a.txt', '/home/u/docs'], 'C:\\Загрузки', 'skip')

    api.FilesDownload.mockClear()
    p = t.download()
    await flush()
    t.conflict!.resolve(null)
    expect(await p).toBe('')
    expect(t.conflict).toBeNull()
    expect(api.FilesDownload).not.toHaveBeenCalled()
  })

  it('takes Explorer drops of its own tab only, into the folder dropped on', async () => {
    await open('t1')
    await open('t2')
    api.FilesConflicts.mockResolvedValue([])
    api.FilesUpload.mockResolvedValue('j')
    handlers.get('files:drop')!({ tab: 't2', dir: '/home/u/docs', paths: ['C:\\x', 'C:\\y'] })
    await flush()
    await flush()
    expect(api.FilesUpload).toHaveBeenCalledTimes(1)
    expect(api.FilesUpload).toHaveBeenCalledWith('t2', ['C:\\x', 'C:\\y'], '/home/u/docs', 'skip')
    disposeFilesTab('t2')
  })

  it('shows a failed check as a toast and queues nothing', async () => {
    const t = await open()
    api.FilesConflicts.mockRejectedValue('Нет связи')
    expect(await t.transfer('upload', ['C:\\a'], '/home/u')).toBe('')
    expect(toastState.message).toBe('Нет связи')
    expect(api.FilesUpload).not.toHaveBeenCalled()
  })

  it('tracks jobs from files:job, their percent and whether the tab is busy', async () => {
    const t = await open()
    const job = (over: Partial<Job>) => ({ id: 'j1', kind: 'upload', title: 'a', state: 'running', message: '', detail: '', total_bytes: 200, done_bytes: 90, total_files: 1, done_files: 0, failed: 0, renamed: 0, current: '', speed_bps: 0, dest: '', retry_in_s: 0, ...over })
    expect(t.progress).toBeNull()
    handlers.get('files:job:t1')!(job({}))
    expect(t.busy).toBe(true)
    expect(t.progress).toBe(45)
    handlers.get('files:job:t1')!(job({ state: 'waiting', retry_in_s: 5 }))
    expect(t.jobs).toHaveLength(1)
    expect(t.jobs[0].retryAt).toBeGreaterThan(Date.now())
    handlers.get('files:job:t1')!(job({ state: 'done', done_bytes: 200 }))
    expect(t.busy).toBe(false)
    expect(t.progress).toBeNull()
    api.FilesJobRemove.mockResolvedValue(undefined)
    await t.clearFinished()
    expect(api.FilesJobRemove).toHaveBeenCalledWith('t1', 'j1')
    expect(t.jobs).toEqual([])
  })
})

describe('FilesTab login', () => {
  it('holds login output until the terminal attaches, then streams it', async () => {
    api.FilesHome.mockRejectedValue('нет подключения')
    const t = await open()
    expect(t.loginSeen).toBe(false)
    handlers.get('files:login:t1')!(btoa('Password: '))
    expect(t.loginSeen).toBe(true)
    const got: string[] = []
    const detach = t.attachLogin((b) => got.push(new TextDecoder().decode(b)))
    handlers.get('files:login:t1')!(btoa('*'))
    expect(got).toEqual(['Password: ', '*'])
    detach()
    api.FilesHome.mockResolvedValue('/home/u')
    t.onState('connected')
    expect(t.loginSeen).toBe(false)
  })

  it('a retry starts a fresh login terminal', async () => {
    const t = await open()
    api.FilesRetry.mockResolvedValue(undefined)
    const epoch = t.loginEpoch
    await t.retry()
    expect(api.FilesRetry).toHaveBeenCalledWith('t1')
    expect(t.loginEpoch).toBe(epoch + 1)
  })

  it('forgets the tab and its events on dispose', async () => {
    await open()
    disposeFilesTab('t1')
    expect(filesTab('t1')).toBeUndefined()
    expect(handlers.has('files:job:t1')).toBe(false)
    expect(handlers.has('files:drop')).toBe(false)
  })
})
