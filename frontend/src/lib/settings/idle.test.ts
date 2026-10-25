import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  VaultLock: vi.fn(() => Promise.resolve()),
  VaultState: vi.fn(() => Promise.resolve({ locked: true, has_password: true, corrupt: false, error: '' })),
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})

import { hosts, keys, vault } from '../stores.svelte'
import { createIdleLock } from './idle'
import { lockVault } from '../stores.svelte'

const minute = 60_000

describe('idle lock', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
  })
  afterEach(() => vi.useRealTimers())

  it('calls VaultLock after the configured minutes without activity', async () => {
    keys.splice(0, keys.length, { id: 'k' } as never)
    hosts.splice(0, hosts.length, { id: 'h' } as never)
    const idle = createIdleLock(lockVault)
    idle.start(5 * minute)
    await vi.advanceTimersByTimeAsync(5 * minute - 1000)
    expect(api.VaultLock).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.VaultLock).toHaveBeenCalledTimes(1)
    expect(vault.locked).toBe(true)
    expect(keys).toHaveLength(0)
    expect(hosts).toHaveLength(0)
    // Locked: the timer is not re-armed.
    await vi.advanceTimersByTimeAsync(30 * minute)
    expect(api.VaultLock).toHaveBeenCalledTimes(1)
  })

  it('resets on keyboard, mouse and wheel activity', async () => {
    const idle = createIdleLock(lockVault)
    idle.start(5 * minute)
    for (const type of ['keydown', 'mousedown', 'wheel']) {
      await vi.advanceTimersByTimeAsync(4 * minute)
      window.dispatchEvent(new Event(type))
    }
    await vi.advanceTimersByTimeAsync(4 * minute)
    expect(api.VaultLock).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(minute)
    expect(api.VaultLock).toHaveBeenCalledTimes(1)
  })

  it('does nothing when stopped or set to 0', async () => {
    const idle = createIdleLock(lockVault)
    idle.start(0)
    await vi.advanceTimersByTimeAsync(60 * minute)
    idle.start(5 * minute)
    idle.stop()
    await vi.advanceTimersByTimeAsync(60 * minute)
    expect(api.VaultLock).not.toHaveBeenCalled()
  })
})
