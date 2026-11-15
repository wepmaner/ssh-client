// State of the file tabs: one FilesTab per open tab, fed by the files:* events of Go.
import {
  EventsOn,
  FilesConflicts,
  FilesDelete,
  FilesDownload,
  FilesHome,
  FilesJobCancel,
  FilesJobRemove,
  FilesJobRetry,
  FilesList,
  FilesLoginAttach,
  FilesMkdir,
  FilesMove,
  FilesPickDownloadDir,
  FilesPickUpload,
  FilesRename,
  FilesRetry,
  FilesUpload,
  errorText,
} from '../api'
import { toast } from '../stores.svelte'
import { decodeBase64 } from '../terminal/base64'
import { baseName, compareEntries, isHidden, joinPath, parentPath, type Entry, type Sort } from './format'
import { emptySelection, pruneSelection, type Selection } from './selection'

export type JobKind = 'upload' | 'download' | 'delete'
export type JobState = 'queued' | 'running' | 'waiting' | 'done' | 'failed' | 'cancelled'

/**
 * One job of the tab's queue, see app.FilesJobDTO; retryAt is when a waiting job tries again,
 * finishedAt when the view saw it finish (0 for one finished before the view listened).
 */
export interface Job {
  id: string
  kind: JobKind
  title: string
  state: JobState
  message: string
  detail: string
  total_bytes: number
  done_bytes: number
  total_files: number
  done_files: number
  failed: number
  renamed: number
  skipped: number
  current: string
  speed_bps: number
  dest: string
  retry_in_s: number
  retryAt: number
  finishedAt: number
}

/** A job as Go sends it; skipped may be missing from an older payload. */
export type JobDTO = Omit<Job, 'retryAt' | 'finishedAt' | 'skipped'> & { skipped?: number }

/** Payload of files:drop, see app.FilesDropDTO. */
export interface DropPayload {
  tab: string
  dir: string
  paths: string[]
}

export type ConflictChoice = 'replace' | 'skip'

/** Data type of an internal drag of rows; Explorer drags carry 'Files' instead. */
export const MOVE_TYPE = 'application/x-sshclient-files'

const LOGIN_BUFFER_LIMIT = 64 * 1024
/** A listing that takes longer gets its loading hint. */
export const SLOW_LISTING_MS = 300

export const isActive = (j: Job) => j.state === 'queued' || j.state === 'running' || j.state === 'waiting'
export const isFinished = (j: Job) => !isActive(j)

const plain = <T>(v: T): T => JSON.parse(JSON.stringify(v))

/** Same folder in the POSIX sense: «/a/b/» is «/a/b». */
const samePath = (a: string, b: string) => (a.length > 1 ? a.replace(/\/+$/, '') : a) === (b.length > 1 ? b.replace(/\/+$/, '') : b)

export class FilesTab {
  readonly id: string
  readonly hostId: string

  /** The folder shown; set at once on navigation, then to the canonical path Go returns. */
  path = $state('')
  entries = $state<Entry[]>([])
  /** A listing was shown at least once: a later drop of the link keeps the list in place. */
  loaded = $state(false)
  listing = $state(false)
  slow = $state(false)
  /** Why the folder could not be read; the path stays, back and up still work. */
  listError = $state('')

  history = $state<string[]>([])
  index = $state(-1)

  sel = $state<Selection>(emptySelection())
  sort = $state<Sort>({ key: 'name', desc: false })
  showHidden = $state(false)
  filter = $state('')

  /** Inline editors: the name being renamed, or a new folder row. */
  renaming = $state('')
  renameError = $state('')
  creating = $state(false)
  createError = $state('')

  jobs = $state<Job[]>([])
  /** The conflict question of a transfer, answered by the dialog. */
  conflict = $state<{ names: string[]; merge: boolean; resolve: (c: ConflictChoice | null) => void } | null>(null)
  /** Names asked to be deleted, confirmed by the dialog. */
  deleteAsk = $state<string[] | null>(null)

  /** Output arrived on the login terminal since the last successful connect. */
  loginSeen = $state(false)
  /** Bumped on every new login: the login card mounts a fresh terminal. */
  loginEpoch = $state(0)

  /** Paths of the rows being dragged inside the list; dragover cannot read the data. */
  dragging: string[] = []

  readonly visible: Entry[] = $derived.by(() => {
    const q = this.filter.trim().toLocaleLowerCase('ru')
    return this.entries
      .filter((e) => (this.showHidden || !isHidden(e.name)) && (!q || e.name.toLocaleLowerCase('ru').includes(q)))
      .sort((a, b) => compareEntries(a, b, this.sort))
  })
  readonly order: string[] = $derived(this.visible.map((e) => e.name))
  readonly selectedEntries: Entry[] = $derived.by(() => {
    const s = new Set(this.sel.selected)
    return this.visible.filter((e) => s.has(e.name))
  })
  readonly canBack = $derived(this.index > 0)
  readonly canForward = $derived(this.index >= 0 && this.index < this.history.length - 1)
  readonly canUp = $derived(this.path !== '' && this.path !== '/')

  readonly busy = $derived(this.jobs.some(isActive))
  /** Percent of the running transfers for the tab label; null when none runs. */
  readonly progress: number | null = $derived.by(() => {
    const active = this.jobs.filter((j) => isActive(j) && j.kind !== 'delete')
    if (active.length === 0) return null
    const total = active.reduce((s, j) => s + j.total_bytes, 0)
    const done = active.reduce((s, j) => s + Math.min(j.done_bytes, j.total_bytes), 0)
    if (total > 0) return Math.floor((done / total) * 100)
    const files = active.reduce((s, j) => s + j.total_files, 0)
    return files > 0 ? Math.floor((active.reduce((s, j) => s + j.done_files, 0) / files) * 100) : 0
  })

  private offs: (() => void)[] = []
  private token = 0
  private slowTimer: ReturnType<typeof setTimeout> | undefined
  private starting: Promise<void> | null = null
  private loginBuf: Uint8Array[] = []
  private loginBytes = 0
  private loginSink: ((b: Uint8Array) => void) | null = null
  private onConnected: () => void

  constructor(id: string, hostId: string, onConnected: () => void = () => {}) {
    this.id = id
    this.hostId = hostId
    this.onConnected = onConnected
    this.offs.push(
      EventsOn('files:job:' + id, (dto: JobDTO) => this.applyJob(dto)),
      EventsOn('files:changed:' + id, (p: { dir: string }) => {
        if (this.loaded && p && samePath(p.dir, this.path)) void this.load(this.path, { keep: true })
      }),
      EventsOn('files:login:' + id, (b64: string) => this.loginData(decodeBase64(b64))),
    )
    // Go holds the login output until someone listens; the buffer keeps it for the card.
    void FilesLoginAttach(id).catch(() => {})
  }

  dispose(): void {
    for (const off of this.offs) off()
    this.offs = []
    clearTimeout(this.slowTimer)
    this.token++
    this.conflict?.resolve(null)
    this.loginSink = null
  }

  // ---- connection and login ----

  /** The tab's connection state changed (files:state). */
  onState(state: string): void {
    if (state === 'connected') {
      this.loginSeen = false
      this.loginBuf = []
      this.loginBytes = 0
      if (this.loaded) void this.load(this.path, { keep: true })
      else void this.start()
    }
  }

  /**
   * Loads the home folder once. Called on open as a probe too: if the host's connection is
   * already up, the listing appears even if the connected event came before we listened.
   */
  start(probe = false): Promise<void> {
    if (this.loaded || this.starting) return this.starting ?? Promise.resolve()
    this.starting = (async () => {
      try {
        const home = await FilesHome(this.id)
        this.onConnected()
        await this.navigate(home || '/')
      } catch (e) {
        if (!probe) this.listError = errorText(e)
      } finally {
        this.starting = null
      }
    })()
    return this.starting
  }

  private loginData(b: Uint8Array): void {
    this.loginSeen = true
    if (this.loginSink) {
      this.loginSink(b)
      return
    }
    this.loginBuf.push(b)
    this.loginBytes += b.length
    while (this.loginBytes > LOGIN_BUFFER_LIMIT && this.loginBuf.length > 1) this.loginBytes -= this.loginBuf.shift()!.length
  }

  /** The login terminal listens: the held output first, then live. Returns the detach function. */
  attachLogin(sink: (b: Uint8Array) => void): () => void {
    for (const b of this.loginBuf) sink(b)
    this.loginSink = sink
    return () => {
      if (this.loginSink === sink) this.loginSink = null
    }
  }

  async retry(): Promise<void> {
    this.loginBuf = []
    this.loginBytes = 0
    this.loginSeen = false
    this.loginEpoch++
    try {
      await FilesRetry(this.id)
      void FilesLoginAttach(this.id).catch(() => {})
    } catch (e) {
      toast(errorText(e))
    }
  }

  // ---- navigation ----

  /** Lists dir. keep holds the selection (a refresh); select picks one name after it. */
  async load(dir: string, opts: { keep?: boolean; select?: string } = {}): Promise<boolean> {
    const token = ++this.token
    this.listing = true
    clearTimeout(this.slowTimer)
    this.slowTimer = setTimeout(() => token === this.token && (this.slow = true), SLOW_LISTING_MS)
    try {
      const res = await FilesList(this.id, dir)
      if (token !== this.token) return false
      this.path = res.path || dir
      if (this.index >= 0) this.history[this.index] = this.path
      this.entries = plain(res.entries ?? [])
      this.listError = ''
      this.loaded = true
      if (opts.select) this.sel = { selected: [opts.select], anchor: opts.select, cursor: opts.select }
      else this.sel = opts.keep ? pruneSelection(this.sel, this.order) : emptySelection()
      return true
    } catch (e) {
      if (token !== this.token) return false
      this.path = dir
      this.entries = []
      this.listError = errorText(e)
      this.loaded = true
      this.sel = emptySelection()
      return false
    } finally {
      if (token === this.token) {
        clearTimeout(this.slowTimer)
        this.listing = false
        this.slow = false
      }
    }
  }

  /** Goes to dir and records it in the history; the forward part is dropped. */
  navigate(dir: string): Promise<boolean> {
    const target = dir.trim() || '/'
    if (this.loaded && samePath(target, this.path) && !this.listError) return this.load(this.path, { keep: true })
    this.history = [...this.history.slice(0, this.index + 1), target]
    this.index = this.history.length - 1
    this.cancelEdits()
    this.filter = ''
    this.path = target
    return this.load(target)
  }

  back(): Promise<boolean> {
    if (!this.canBack) return Promise.resolve(false)
    this.index--
    this.cancelEdits()
    this.filter = ''
    return this.load(this.history[this.index])
  }

  forward(): Promise<boolean> {
    if (!this.canForward) return Promise.resolve(false)
    this.index++
    this.cancelEdits()
    this.filter = ''
    return this.load(this.history[this.index])
  }

  /** Up one level; the folder we came from is selected, as in Explorer. */
  async up(): Promise<boolean> {
    if (!this.canUp) return false
    const from = baseName(this.path)
    const ok = await this.navigate(parentPath(this.path))
    if (ok && this.order.includes(from)) this.sel = { selected: [from], anchor: from, cursor: from }
    return ok
  }

  refresh(): Promise<boolean> {
    return this.load(this.path, { keep: true })
  }

  pathOf(name: string): string {
    return joinPath(this.path, name)
  }

  /** Double click or Enter: a folder opens, a file is downloaded. */
  async open(e: Entry): Promise<void> {
    if (e.dir && !e.broken) await this.navigate(this.pathOf(e.name))
    else if (!e.broken) await this.download([e.name])
  }

  setSort(key: Sort['key']): void {
    this.sort = this.sort.key === key ? { key, desc: !this.sort.desc } : { key, desc: false }
  }

  toggleHidden(): void {
    this.showHidden = !this.showHidden
    this.sel = pruneSelection(this.sel, this.order)
  }

  // ---- changes ----

  cancelEdits(): void {
    this.renaming = ''
    this.renameError = ''
    this.creating = false
    this.createError = ''
  }

  startRename(name: string): void {
    this.cancelEdits()
    this.renaming = name
  }

  /** Renames in place; true when the editor may close. Without overwriting: Go refuses a taken name. */
  async rename(from: string, to: string): Promise<boolean> {
    const name = to.trim()
    if (!name || name === from) {
      this.renaming = ''
      return true
    }
    if (name.includes('/')) {
      this.renameError = 'Имя не может содержать «/»'
      return false
    }
    try {
      const got = await FilesRename(this.id, this.pathOf(from), name)
      this.renaming = ''
      this.renameError = ''
      await this.load(this.path, { select: baseName(got || name) })
      return true
    } catch (e) {
      this.renameError = errorText(e)
      return false
    }
  }

  startCreate(): void {
    this.cancelEdits()
    this.filter = ''
    this.creating = true
  }

  async mkdir(raw: string): Promise<boolean> {
    const name = raw.trim()
    if (!name) {
      this.creating = false
      return true
    }
    if (name.includes('/')) {
      this.createError = 'Имя не может содержать «/»'
      return false
    }
    try {
      const p = await FilesMkdir(this.id, this.path, name)
      this.creating = false
      this.createError = ''
      await this.load(this.path, { select: baseName(p || name) })
      return true
    } catch (e) {
      this.createError = errorText(e)
      return false
    }
  }

  /** Moves the rows by path into destDir; a folder never goes into itself or where it already is. */
  async move(paths: string[], destDir: string): Promise<void> {
    const list = paths.filter((p) => !samePath(parentPath(p), destDir) && !samePath(p, destDir) && !destDir.startsWith(p + '/'))
    if (list.length === 0) return
    try {
      await FilesMove(this.id, list, destDir)
    } catch (e) {
      toast(errorText(e))
    }
    await this.load(this.path, { keep: true })
  }

  askDelete(names: string[]): void {
    if (names.length > 0) this.deleteAsk = names
  }

  async confirmDelete(): Promise<void> {
    const names = this.deleteAsk ?? []
    this.deleteAsk = null
    if (names.length === 0) return
    try {
      await FilesDelete(
        this.id,
        names.map((n) => this.pathOf(n)),
      )
      this.sel = emptySelection()
    } catch (e) {
      toast(errorText(e))
    }
  }

  // ---- transfers ----

  /** Asks the conflict dialog; null is «Отмена». merge: a clash is a folder that merges. */
  askConflict(names: string[], merge = false): Promise<ConflictChoice | null> {
    this.conflict?.resolve(null)
    return new Promise((resolve) => {
      this.conflict = {
        names,
        merge,
        resolve: (c) => {
          this.conflict = null
          resolve(c)
        },
      }
    })
  }

  /** Checks the top-level names, asks about clashes, then queues the job. Returns its id or ''. */
  async transfer(direction: 'upload' | 'download', sources: string[], dest: string): Promise<string> {
    if (sources.length === 0 || !dest) return ''
    try {
      const clashes = (await FilesConflicts(this.id, direction, sources, dest)) ?? []
      let mode: ConflictChoice = 'skip'
      if (clashes.length > 0) {
        const choice = await this.askConflict(
          clashes.map((c) => c.name),
          clashes.some((c) => c.merge),
        )
        if (!choice) return ''
        mode = choice
      }
      return direction === 'upload' ? await FilesUpload(this.id, sources, dest, mode) : await FilesDownload(this.id, sources, dest, mode)
    } catch (e) {
      toast(errorText(e))
      return ''
    }
  }

  async upload(folder: boolean, dest = this.path): Promise<string> {
    let local: string[]
    try {
      local = (await FilesPickUpload(folder)) ?? []
    } catch (e) {
      toast(errorText(e))
      return ''
    }
    return local.length > 0 ? this.transfer('upload', local, dest) : ''
  }

  /** Downloads the named entries of the current folder (by default the selection). */
  async download(names = this.sel.selected): Promise<string> {
    if (names.length === 0) return ''
    const paths = names.map((n) => this.pathOf(n))
    let dir: string
    try {
      dir = await FilesPickDownloadDir()
    } catch (e) {
      toast(errorText(e))
      return ''
    }
    return dir ? this.transfer('download', paths, dir) : ''
  }

  /** Files dropped from Explorer on this tab's list or on one of its folders. */
  drop(p: DropPayload): Promise<string> {
    if (p.tab !== this.id || !p.paths?.length) return Promise.resolve('')
    return this.transfer('upload', p.paths, p.dir || this.path)
  }

  applyJob(dto: JobDTO): void {
    const i = this.jobs.findIndex((j) => j.id === dto.id)
    const prev = i >= 0 ? this.jobs[i] : undefined
    const job: Job = {
      ...plain(dto),
      skipped: dto.skipped ?? 0,
      retryAt: dto.state === 'waiting' && dto.retry_in_s > 0 ? Date.now() + dto.retry_in_s * 1000 : 0,
      finishedAt: 0,
    }
    if (isFinished(job)) job.finishedAt = prev && isFinished(prev) ? prev.finishedAt : prev ? Date.now() : 0
    if (i >= 0) this.jobs[i] = job
    else this.jobs.push(job)
  }

  async cancelJob(id: string): Promise<void> {
    await FilesJobCancel(this.id, id).catch((e) => toast(errorText(e)))
  }

  async retryJob(id: string): Promise<void> {
    await FilesJobRetry(this.id, id).catch((e) => toast(errorText(e)))
  }

  async removeJob(id: string): Promise<void> {
    try {
      await FilesJobRemove(this.id, id)
      this.jobs = this.jobs.filter((j) => j.id !== id)
    } catch (e) {
      toast(errorText(e))
    }
  }

  async clearFinished(): Promise<void> {
    const done = this.jobs.filter(isFinished)
    const removed = new Set<string>()
    await Promise.all(
      done.map((j) =>
        FilesJobRemove(this.id, j.id)
          .then(() => removed.add(j.id))
          .catch(() => {}),
      ),
    )
    this.jobs = this.jobs.filter((j) => !removed.has(j.id))
  }
}

const tabs = new Map<string, FilesTab>()
let dropOff: (() => void) | null = null

/** Creates the state of a new file tab; onConnected marks the tab connected when a probe finds the link up. */
export function createFilesTab(id: string, hostId: string, onConnected?: () => void): FilesTab {
  const t = new FilesTab(id, hostId, onConnected)
  tabs.set(id, t)
  dropOff ??= EventsOn('files:drop', (p: DropPayload) => {
    if (p) void tabs.get(p.tab)?.drop(p)
  })
  void t.start(true)
  return t
}

export function filesTab(id: string): FilesTab | undefined {
  return tabs.get(id)
}

export function disposeFilesTab(id: string): void {
  tabs.get(id)?.dispose()
  tabs.delete(id)
  if (tabs.size === 0 && dropOff) {
    dropOff()
    dropOff = null
  }
}
