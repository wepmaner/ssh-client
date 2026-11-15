// Texts of the transfer queue.
import type { Job } from './files.svelte'
import { formatSize, formatSpeed } from './format'

const active = (j: Job) => j.state === 'queued' || j.state === 'running' || j.state === 'waiting'

/** Seconds left before a waiting job tries again. */
export function retryLeft(j: Job, now: number): number {
  if (j.retryAt > 0) return Math.max(0, Math.ceil((j.retryAt - now) / 1000))
  return Math.max(0, j.retry_in_s)
}

/** Fraction done in 0..1: by bytes, else by files. */
export function jobFraction(j: Job): number {
  if (j.state === 'done') return 1
  if (j.total_bytes > 0) return Math.min(1, j.done_bytes / j.total_bytes)
  if (j.total_files > 0) return Math.min(1, j.done_files / j.total_files)
  return 0
}

function tail(j: Job): string {
  const parts: string[] = []
  if (j.failed > 0) parts.push(`с ошибками: ${j.failed}`)
  if (j.renamed > 0) parts.push(`переименовано: ${j.renamed}`)
  return parts.join(', ')
}

/** The state line of one job. */
export function jobStatus(j: Job, now: number): string {
  switch (j.state) {
    case 'queued':
      return 'В очереди'
    case 'waiting':
      return `Связь прервана, повтор через ${retryLeft(j, now)} с`
    case 'running': {
      if (j.kind === 'delete') return j.total_files > 0 ? `Удалено ${j.done_files} из ${j.total_files}` : 'Удаление…'
      if (j.total_bytes <= 0 && j.total_files <= 0) return 'Подготовка…'
      const speed = formatSpeed(j.speed_bps)
      return [`${formatSize(j.done_bytes)} из ${formatSize(j.total_bytes)}`, speed].filter(Boolean).join(' · ')
    }
    case 'done': {
      const t = tail(j)
      return t ? `Готово, ${t}` : 'Готово'
    }
    case 'failed':
      return j.message ? `Ошибка: ${j.message}` : 'Ошибка'
    case 'cancelled':
      return 'Отменено'
  }
  return ''
}

/** The summary row: «Передаётся 2 из 5 · 12,3 МБ из 40 МБ · 3,2 МБ/с». */
export function queueSummary(jobs: Job[], now: number): string {
  const run = jobs.filter(active)
  if (run.length === 0) {
    const failed = jobs.filter((j) => j.state === 'failed').length
    const withErrors = jobs.filter((j) => j.state === 'done' && j.failed > 0).length
    if (failed > 0) return `Передачи завершены, ошибок: ${failed}`
    if (withErrors > 0) return 'Передачи завершены, с ошибками'
    return 'Передачи завершены'
  }
  const waiting = run.find((j) => j.state === 'waiting')
  if (waiting) return `Связь прервана, повтор через ${retryLeft(waiting, now)} с`
  const transfers = run.filter((j) => j.kind !== 'delete')
  if (transfers.length === 0) return 'Удаление…'
  const files = transfers.reduce((s, j) => s + j.total_files, 0)
  const doneFiles = transfers.reduce((s, j) => s + j.done_files, 0)
  const total = transfers.reduce((s, j) => s + j.total_bytes, 0)
  const done = transfers.reduce((s, j) => s + Math.min(j.done_bytes, j.total_bytes), 0)
  const speed = transfers.reduce((s, j) => s + (j.state === 'running' ? j.speed_bps : 0), 0)
  const parts = [files > 0 ? `Передаётся ${Math.min(files, doneFiles + 1)} из ${files}` : 'Подготовка передачи']
  if (total > 0) parts.push(`${formatSize(done)} из ${formatSize(total)}`)
  if (speed > 0) parts.push(formatSpeed(speed))
  return parts.join(' · ')
}
