import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../api', () => ({ FilesDragOut: vi.fn(() => Promise.resolve()), errorText: String }))
const toast = vi.fn()
vi.mock('../stores.svelte', () => ({ toast: (m: string) => toast(m) }))

import { FilesDragOut } from '../api'
import { watchDragOut } from './dragout'

describe('watchDragOut', () => {
  afterEach(() => toast.mockReset())

  it('hands the drag to Go as it starts', async () => {
    await watchDragOut('t1', ['/a', '/b'])
    expect(FilesDragOut).toHaveBeenCalledExactlyOnceWith('t1', ['/a', '/b'])
    expect(toast).not.toHaveBeenCalled()
  })

  it('shows the error of Go', async () => {
    const out = vi.fn(() => Promise.reject('Слишком много файлов'))
    await watchDragOut('t1', ['/a'], out)
    expect(toast).toHaveBeenCalledWith('Слишком много файлов')
  })
})
