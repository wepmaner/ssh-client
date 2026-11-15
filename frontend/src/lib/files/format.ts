// Formatting and POSIX path helpers of the file manager, in the manner of Explorer.

/** One entry of a remote folder, see app.FilesEntryDTO. mtime is unix ms. */
export interface Entry {
  name: string
  dir: boolean
  link: boolean
  broken: boolean
  size: number
  mtime: number
  mode: string
}

export type SortKey = 'name' | 'mtime' | 'size' | 'mode'
export interface Sort {
  key: SortKey
  desc: boolean
}

const units = ['КБ', 'МБ', 'ГБ', 'ТБ', 'ПБ']

/** Explorer keeps three significant digits and cuts, never rounds up: 1023 bytes is «0,99 КБ». */
function cut(v: number, digits: number): string {
  const f = 10 ** digits
  const s = (Math.floor(v * f + 1e-9) / f).toFixed(digits)
  return s.replace(/\.?0+$/, '').replace('.', ',')
}

/** «0 байт», «512 байт», «12,3 КБ», «999 КБ», «1,5 ГБ». */
export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return ''
  if (bytes < 1000) return `${Math.floor(bytes)} байт`
  let v = bytes / 1024
  let u = 0
  while (v >= 1000 && u < units.length - 1) {
    v /= 1024
    u++
  }
  const digits = v < 10 ? 2 : v < 100 ? 1 : 0
  return `${cut(v, digits)} ${units[u]}`
}

/** «3,2 МБ/с». */
export function formatSpeed(bps: number): string {
  return bps > 0 ? `${formatSize(bps)}/с` : ''
}

const pad = (n: number) => String(n).padStart(2, '0')

/** «15.11.2026 14:05» in local time; empty for an unknown time. */
export function formatDate(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  return `${pad(d.getDate())}.${pad(d.getMonth() + 1)}.${d.getFullYear()} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** Russian plural: one (1, 21), few (2–4, 22–24), many. */
export function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10
  const m100 = n % 100
  if (m10 === 1 && m100 !== 11) return one
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few
  return many
}

export const objectsLabel = (n: number) => `${n} ${plural(n, 'объект', 'объекта', 'объектов')}`

/** Joins a remote folder and a name: «/» + «etc» is «/etc», «/home/u» + «a» is «/home/u/a». */
export function joinPath(dir: string, name: string): string {
  if (!dir) return name
  return dir.endsWith('/') ? dir + name : `${dir}/${name}`
}

/** Parent of a remote path; the root is its own parent. */
export function parentPath(p: string): string {
  const s = p.length > 1 ? p.replace(/\/+$/, '') : p
  const i = s.lastIndexOf('/')
  if (i <= 0) return '/'
  return s.slice(0, i)
}

/** Last segment of a remote path. */
export function baseName(p: string): string {
  const s = p.length > 1 ? p.replace(/\/+$/, '') : p
  return s.slice(s.lastIndexOf('/') + 1) || '/'
}

export interface Crumb {
  name: string
  path: string
}

/** Breadcrumbs of an absolute path: «/», «home», «u». */
export function crumbs(p: string): Crumb[] {
  if (!p) return []
  const out: Crumb[] = [{ name: '/', path: '/' }]
  let acc = ''
  for (const part of p.split('/').filter(Boolean)) {
    acc += '/' + part
    out.push({ name: part, path: acc })
  }
  return out
}

/** Hidden in the POSIX sense: the name starts with a dot. */
export const isHidden = (name: string) => name.startsWith('.')

/** A link to a folder opens as a folder; Go reports dir for its target. */
export const opensAsFolder = (e: Entry) => e.dir && !e.broken

const collator = new Intl.Collator('ru', { numeric: true, sensitivity: 'base' })

/** Folders always first, then by the column; ties fall back to the natural name order. */
export function compareEntries(a: Entry, b: Entry, sort: Sort): number {
  if (a.dir !== b.dir) return a.dir ? -1 : 1
  let r = 0
  if (sort.key === 'mtime') r = a.mtime - b.mtime
  else if (sort.key === 'size') r = a.dir ? 0 : a.size - b.size
  else if (sort.key === 'mode') r = collator.compare(a.mode, b.mode)
  if (r === 0) r = collator.compare(a.name, b.name)
  if (r === 0) r = a.name < b.name ? -1 : a.name > b.name ? 1 : 0
  return sort.desc ? -r : r
}

/** Range of the name to select for a rename: without the extension, as Explorer does. */
export function stemRange(name: string, dir: boolean): [number, number] {
  if (dir) return [0, name.length]
  const i = name.lastIndexOf('.')
  return i > 0 ? [0, i] : [0, name.length]
}
