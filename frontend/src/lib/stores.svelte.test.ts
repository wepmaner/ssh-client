import { describe, expect, it, vi } from 'vitest'

const { getSettings, vaultState, keysList, vaultLock } = vi.hoisted(() => ({
  getSettings: vi.fn(),
  vaultState: vi.fn(),
  keysList: vi.fn(() => Promise.resolve([])),
  vaultLock: vi.fn(() => Promise.resolve()),
}))
vi.mock('./api', async () => {
  const model = await import('../../bindings/sshclient/internal/model/models')
  return {
    GetSettings: getSettings,
    HostsList: () => Promise.resolve([]),
    GroupsList: () => Promise.resolve([]),
    KeysList: keysList,
    VaultState: vaultState,
    VaultLock: vaultLock,
    errorText: String,
    model,
  }
})

import { forwards } from './forwards/forwards.svelte'
import { hosts, lockVault, refreshAll, refreshSettings, settings, vault } from './stores.svelte'

describe('stores', () => {
  it('settings changes notify subscribers after refreshSettings', async () => {
    const seen: number[] = []
    const stop = $effect.root(() => {
      $effect(() => {
        seen.push(settings.terminal_size)
      })
    })
    await Promise.resolve()
    const model = await import('../../bindings/sshclient/internal/model/models')
    getSettings.mockResolvedValue(model.Settings.createFrom({ terminal_size: 17, window: { w: 5 } }))
    await refreshSettings()
    await Promise.resolve()
    expect(seen.at(-1)).toBe(17)
    stop()
  })

  it('refreshAll turns a rejected VaultState into a visible error', async () => {
    vaultState.mockRejectedValue('boom')
    await refreshAll()
    expect(vault.ready).toBe(true)
    expect(vault.error).toBe('boom')
  })

  it('the idle lock drops the forwards with the hosts', async () => {
    hosts.push({ id: 'h1', name: 'prod-1' } as never)
    forwards.list.push({ id: 'f1', name: 'Postgres', host_name: 'prod-1', listen: '127.0.0.1:5432' } as never)
    vaultState.mockResolvedValue({ locked: true, has_password: true, corrupt: false })
    await lockVault()
    expect(vaultLock).toHaveBeenCalled()
    expect(hosts).toHaveLength(0)
    expect(forwards.list).toHaveLength(0)
  })
})
