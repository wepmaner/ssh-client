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

function tail(j: Job): string[] {
  const parts: string[] = []
  if (j.failed > 0) parts.push(`ошибок: ${j.failed}`)
  if (j.renamed > 0) parts.push(`переименовано: ${j.renamed}`)
  return parts
}

const activeRank = (j: Job) => (j.state === 'queued' ? 1 : 0)

/**
 * The rows of the queue: running and waiting jobs, then queued ones in queue order, then the finished
 * ones newest first (those finished before the view listened by queue order, newest first too).
 */
export function orderJobs(jobs: Job[]): Job[] {
  const at = new Map(jobs.map((j, i) => [j.id, i]))
  const idx = (j: Job) => at.get(j.id) ?? 0
  const live = jobs.filter(active).sort((a, b) => activeRank(a) - activeRank(b) || idx(a) - idx(b))
  const done = jobs.filter((j) => !active(j)).sort((a, b) => b.finishedAt - a.finishedAt || idx(b) - idx(a))
  return [...live, ...done]
}

/** The short result of a finished job: «572 МБ · ошибок: 1», «Отменено», the error. */
export function jobResult(j: Job): string {
  switch (j.state) {
    case 'done': {
      let head = ''
      if (j.kind === 'delete') head = j.done_files > 0 ? `Удалено: ${j.done_files}` : 'Удалено'
      else if (j.total_bytes > 0) head = formatSize(j.total_bytes)
      else head = 'Готово'
      return [head, ...tail(j)].join(' · ')
    }
    case 'failed':
      return j.message || 'Ошибка'
    case 'cancelled':
      return 'Отменено'
  }
  return ''
}

/** The state line of one job. */
export function jobStatus(j: Job, now: number): string {
  switch (j.state) {
    case 'queued':
      return 'В очереди'
    case 'waiting': {
      const left = retryLeft(j, now)
      return left > 0 ? `Связь прервана, повтор через ${left} с` : 'Переподключение…'
    }
    case 'running': {
      if (j.kind === 'delete') return j.total_files > 0 ? `Удалено ${j.done_files} из ${j.total_files}` : 'Удаление…'
      if (j.total_bytes <= 0 && j.total_files <= 0) return 'Подготовка…'
      const speed = formatSpeed(j.speed_bps)
      return [`${formatSize(j.done_bytes)} из ${formatSize(j.total_bytes)}`, speed].filter(Boolean).join(' · ')
    }
    case 'done':
    case 'failed':
    case 'cancelled':
      return jobResult(j)
  }
  return ''
}

/**
 * The summary row while something runs: «Передаётся 2 из 5 · 12,3 МБ из 40 МБ · 3,2 МБ/с». With
 * everything finished there is none: the rows tell how each ended.
 */
export function queueSummary(jobs: Job[], now: number): string {
  const run = jobs.filter(active)
  if (run.length === 0) return ''
  const waiting = run.find((j) => j.state === 'waiting')
  if (waiting) return jobStatus(waiting, now)
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
