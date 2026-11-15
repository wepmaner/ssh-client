import { describe, expect, it } from 'vitest'
import type { Job } from './files.svelte'
import { baseName, compareEntries, crumbs, formatDate, formatSize, formatSpeed, joinPath, objectsLabel, parentPath, stemRange, type Entry } from './format'
import { jobStatus, queueSummary, retryLeft } from './jobs'

const entry = (name: string, over: Partial<Entry> = {}): Entry => ({ name, dir: false, link: false, broken: false, size: 0, mtime: 0, mode: '-rw-r--r--', ...over })

describe('formatSize', () => {
  it('writes sizes as Explorer does, in Russian', () => {
    expect(formatSize(0)).toBe('0 байт')
    expect(formatSize(512)).toBe('512 байт')
    expect(formatSize(999)).toBe('999 байт')
    expect(formatSize(1024)).toBe('1 КБ')
    expect(formatSize(1536)).toBe('1,5 КБ')
    expect(formatSize(12.34 * 1024 * 1024)).toBe('12,3 МБ')
    expect(formatSize(999 * 1024)).toBe('999 КБ')
    expect(formatSize(1000 * 1024)).toBe('0,97 МБ')
    expect(formatSize(1.5 * 1024 ** 3)).toBe('1,5 ГБ')
  })

  it('cuts instead of rounding up', () => {
    expect(formatSize(1023 * 1024 + 1023)).toBe('0,99 МБ')
    expect(formatSize(19.99 * 1024)).toBe('19,9 КБ')
  })

  it('formats a speed', () => {
    expect(formatSpeed(3.2 * 1024 * 1024)).toBe('3,2 МБ/с')
    expect(formatSpeed(0)).toBe('')
  })
})

describe('formatDate', () => {
  it('uses dd.mm.yyyy hh:mm in local time', () => {
    expect(formatDate(new Date(2026, 10, 15, 14, 5).getTime())).toBe('15.11.2026 14:05')
    expect(formatDate(new Date(2026, 0, 3, 9, 7).getTime())).toBe('03.01.2026 09:07')
    expect(formatDate(0)).toBe('')
  })
})

describe('paths', () => {
  it('joins and splits POSIX paths', () => {
    expect(joinPath('/', 'etc')).toBe('/etc')
    expect(joinPath('/home/u', 'a b')).toBe('/home/u/a b')
    expect(parentPath('/home/u')).toBe('/home')
    expect(parentPath('/home')).toBe('/')
    expect(parentPath('/')).toBe('/')
    expect(parentPath('/a/b/')).toBe('/a')
    expect(baseName('/home/u')).toBe('u')
    expect(baseName('/')).toBe('/')
  })

  it('makes breadcrumbs', () => {
    expect(crumbs('/home/u')).toEqual([
      { name: '/', path: '/' },
      { name: 'home', path: '/home' },
      { name: 'u', path: '/home/u' },
    ])
    expect(crumbs('/')).toEqual([{ name: '/', path: '/' }])
    expect(crumbs('')).toEqual([])
  })

  it('selects the stem of a file name for a rename', () => {
    expect(stemRange('report.tar.gz', false)).toEqual([0, 10])
    expect(stemRange('.bashrc', false)).toEqual([0, 7])
    expect(stemRange('dir.d', true)).toEqual([0, 5])
  })

  it('counts objects in Russian', () => {
    expect(objectsLabel(1)).toBe('1 объект')
    expect(objectsLabel(3)).toBe('3 объекта')
    expect(objectsLabel(11)).toBe('11 объектов')
    expect(objectsLabel(21)).toBe('21 объект')
  })
})

describe('compareEntries', () => {
  const list = [entry('b10.txt', { size: 5, mtime: 3 }), entry('Docs', { dir: true, mtime: 1 }), entry('b2.txt', { size: 50, mtime: 2 }), entry('ёж'), entry('Арбуз'), entry('apps', { dir: true, mtime: 9 })]
  const names = (key: 'name' | 'size' | 'mtime', desc = false) => [...list].sort((a, b) => compareEntries(a, b, { key, desc })).map((e) => e.name)

  // The ru collation puts Cyrillic before Latin and treats ё as е.
  it('puts folders first and orders names naturally, Russian included', () => {
    expect(names('name')).toEqual(['apps', 'Docs', 'Арбуз', 'ёж', 'b2.txt', 'b10.txt'])
  })

  it('keeps folders first when sorted descending or by another column', () => {
    expect(names('name', true)).toEqual(['Docs', 'apps', 'b10.txt', 'b2.txt', 'ёж', 'Арбуз'])
    expect(names('size')).toEqual(['apps', 'Docs', 'Арбуз', 'ёж', 'b10.txt', 'b2.txt'])
    expect(names('mtime', true).slice(0, 2)).toEqual(['apps', 'Docs'])
  })
})

describe('job texts', () => {
  const job = (over: Partial<Job>): Job => ({
    id: 'j',
    kind: 'upload',
    title: 'a.txt',
    state: 'running',
    message: '',
    detail: '',
    total_bytes: 40 * 1024 * 1024,
    done_bytes: 12.3 * 1024 * 1024,
    total_files: 5,
    done_files: 1,
    failed: 0,
    renamed: 0,
    current: '',
    speed_bps: 3.2 * 1024 * 1024,
    dest: '',
    retry_in_s: 0,
    retryAt: 0,
    ...over,
  })

  it('summarises the queue', () => {
    expect(queueSummary([job({})], 0)).toBe('Передаётся 2 из 5 · 12,3 МБ из 40 МБ · 3,2 МБ/с')
    expect(queueSummary([job({ state: 'done' })], 0)).toBe('Передачи завершены')
    expect(queueSummary([job({ state: 'failed' })], 0)).toBe('Передачи завершены, ошибок: 1')
  })

  it('counts a waiting job down', () => {
    const w = job({ state: 'waiting', retry_in_s: 5, retryAt: 10_000 })
    expect(retryLeft(w, 7_100)).toBe(3)
    expect(jobStatus(w, 7_100)).toBe('Связь прервана, повтор через 3 с')
    expect(queueSummary([w], 7_100)).toBe('Связь прервана, повтор через 3 с')
  })

  it('reports errors and renames in the result', () => {
    expect(jobStatus(job({ state: 'done', failed: 2, renamed: 1 }), 0)).toBe('Готово, с ошибками: 2, переименовано: 1')
    expect(jobStatus(job({ state: 'done' }), 0)).toBe('Готово')
    expect(jobStatus(job({ state: 'failed', message: 'нет прав' }), 0)).toBe('Ошибка: нет прав')
    expect(jobStatus(job({ state: 'queued' }), 0)).toBe('В очереди')
  })
})
