import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../api', () => ({ FilesDragOut: vi.fn(), errorText: String }))
const toast = vi.fn()
vi.mock('../stores.svelte', () => ({ toast: (m: string) => toast(m) }))

import { LEAVE_DELAY, watchDragOut } from './dragout'

function fire(type: string, relatedTarget: EventTarget | null = null): void {
  const e = new Event(type, { bubbles: true })
  Object.defineProperty(e, 'relatedTarget', { value: relatedTarget })
  document.body.dispatchEvent(e)
}

describe('watchDragOut', () => {
  let stop: (() => void) | undefined
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {
    stop?.()
    vi.useRealTimers()
    toast.mockReset()
  })

  it('hands the drag to Go after it left the window, once while Go handles it', async () => {
    let done = () => {}
    const out = vi.fn(() => new Promise<void>((r) => (done = r)))
    stop = watchDragOut('t1', ['/a', '/b'], document, out)
    fire('dragleave')
    vi.advanceTimersByTime(LEAVE_DELAY + 1)
    expect(out).toHaveBeenCalledExactlyOnceWith('t1', ['/a', '/b'])
    fire('dragleave')
    vi.advanceTimersByTime(LEAVE_DELAY + 1)
    expect(out).toHaveBeenCalledTimes(1)
    // Go did nothing (the cursor was back): the next leave hands it over again.
    done()
    await vi.advanceTimersByTimeAsync(0)
    fire('dragleave')
    vi.advanceTimersByTime(LEAVE_DELAY + 1)
    expect(out).toHaveBeenCalledTimes(2)
  })

  it('ignores moves between elements and a drag that comes back', () => {
    const out = vi.fn(() => Promise.resolve())
    stop = watchDragOut('t1', ['/a'], document, out)
    fire('dragleave', document.body)
    vi.advanceTimersByTime(LEAVE_DELAY + 1)
    fire('dragleave')
    vi.advanceTimersByTime(LEAVE_DELAY / 2)
    fire('dragover')
    vi.advanceTimersByTime(LEAVE_DELAY + 1)
    expect(out).not.toHaveBeenCalled()
  })

  it('stops at dragend and drop', () => {
    const out = vi.fn(() => Promise.resolve())
    watchDragOut('t1', ['/a'], document, out)
    fire('dragleave')
    fire('dragend')
    vi.advanceTimersByTime(LEAVE_DELAY + 1)
    watchDragOut('t1', ['/a'], document, out)
    fire('drop')
    fire('dragleave')
    vi.advanceTimersByTime(LEAVE_DELAY + 1)
    expect(out).not.toHaveBeenCalled()
  })

  it('shows the error of Go', async () => {
    const out = vi.fn(() => Promise.reject('Слишком много файлов'))
    stop = watchDragOut('t1', ['/a'], document, out)
    fire('dragleave')
    await vi.advanceTimersByTimeAsync(LEAVE_DELAY + 1)
    expect(toast).toHaveBeenCalledWith('Слишком много файлов')
  })
})
