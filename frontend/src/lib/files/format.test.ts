import { describe, expect, it } from 'vitest'
import type { Job } from './files.svelte'
import { baseName, compareEntries, crumbs, formatDate, formatSize, formatSpeed, joinPath, objectsLabel, parentPath, stemRange, type Entry } from './format'
import { jobResult, jobStatus, orderJobs, queueSummary, retryLeft } from './jobs'

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

  it('keeps the zeros of a whole number', () => {
    expect(formatSize(200_000_000)).toBe('190 МБ') // 190,7 МБ, cut to three digits
    expect(formatSize(100 * 1024)).toBe('100 КБ')
    expect(formatSize(10 * 1024)).toBe('10 КБ')
    expect(formatSize(100)).toBe('100 байт')
    expect(formatSize(10 * 1024 ** 3)).toBe('10 ГБ')
    expect(formatSize(1000 * 1024 * 1024 - 1)).toBe('999 МБ')
    expect(formatSize(1000 * 1024 * 1024)).toBe('0,97 ГБ')
    expect(formatSize(2.05 * 1024 * 1024)).toBe('2,05 МБ')
    expect(formatSize(2.5 * 1024 * 1024)).toBe('2,5 МБ')
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

  // As Explorer on Russian Windows: Latin before Cyrillic, ё next to е.
  it('puts folders first and orders names naturally, Latin before Cyrillic', () => {
    expect(names('name')).toEqual(['apps', 'Docs', 'b2.txt', 'b10.txt', 'Арбуз', 'ёж'])
  })

  it('orders Cyrillic names alphabetically with ё next to е', () => {
    const ru = ['яма', 'ёлка', 'Жук', 'Елена', 'еда', 'Документы', 'empty', 'Ёж', 'file10', 'file2'].map((n) => entry(n))
    expect(ru.sort((a, b) => compareEntries(a, b, { key: 'name', desc: false })).map((e) => e.name)).toEqual([
      'empty',
      'file2',
      'file10',
      'Документы',
      'еда',
      'Ёж',
      'Елена',
      'ёлка',
      'Жук',
      'яма',
    ])
  })

  it('keeps folders first when sorted descending or by another column', () => {
    expect(names('name', true)).toEqual(['Docs', 'apps', 'ёж', 'Арбуз', 'b10.txt', 'b2.txt'])
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
    skipped: 0,
    current: '',
    speed_bps: 3.2 * 1024 * 1024,
    dest: '',
    retry_in_s: 0,
    retryAt: 0,
    finishedAt: 0,
    ...over,
  })

  it('summarises the queue', () => {
    expect(queueSummary([job({})], 0)).toBe('Передаётся 2 из 5 · 12,3 МБ из 40 МБ · 3,2 МБ/с')
    // With everything finished there is no summary: the rows tell how each job ended.
    expect(queueSummary([job({ state: 'done' })], 0)).toBe('')
    expect(queueSummary([job({ state: 'failed' }), job({ id: 'k', state: 'cancelled' })], 0)).toBe('')
  })

  it('orders active jobs first, then finished ones newest first', () => {
    const jobs = [
      job({ id: 'a', state: 'done', finishedAt: 100 }),
      job({ id: 'b', state: 'queued' }),
      job({ id: 'c', state: 'failed', finishedAt: 300 }),
      job({ id: 'd', state: 'running' }),
      job({ id: 'e', state: 'done' }), // finished before the view listened
      job({ id: 'f', state: 'waiting' }),
      job({ id: 'g', state: 'cancelled', finishedAt: 200 }),
    ]
    expect(orderJobs(jobs).map((j) => j.id)).toEqual(['d', 'f', 'b', 'c', 'g', 'a', 'e'])
  })

  it('shows a waiting job that reconnects now', () => {
    expect(jobStatus(job({ state: 'waiting', retry_in_s: 0 }), 0)).toBe('Переподключение…')
  })

  it('counts a waiting job down', () => {
    const w = job({ state: 'waiting', retry_in_s: 5, retryAt: 10_000 })
    expect(retryLeft(w, 7_100)).toBe(3)
    expect(jobStatus(w, 7_100)).toBe('Связь прервана, повтор через 3 с')
    expect(queueSummary([w], 7_100)).toBe('Связь прервана, повтор через 3 с')
  })

  it('reports the size, errors, skipped and renamed files in the result', () => {
    expect(jobStatus(job({ state: 'done', failed: 2, renamed: 1 }), 0)).toBe('40 МБ · ошибок: 2 · переименовано: 1')
    expect(jobResult(job({ state: 'done', skipped: 1 }))).toBe('40 МБ') // skipping was the user's own choice
    expect(jobResult(job({ state: 'done', total_bytes: 0 }))).toBe('Готово')
    expect(jobResult(job({ kind: 'delete', state: 'done', done_files: 4 }))).toBe('Удалено: 4')
    expect(jobStatus(job({ state: 'failed', message: 'нет прав' }), 0)).toBe('нет прав')
    expect(jobStatus(job({ state: 'cancelled' }), 0)).toBe('Отменено')
    expect(jobStatus(job({ state: 'queued' }), 0)).toBe('В очереди')
  })
})
