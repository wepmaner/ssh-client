import { describe, expect, it, vi } from 'vitest'

const { getSettings, vaultState, keysList } = vi.hoisted(() => ({
  getSettings: vi.fn(),
  vaultState: vi.fn(),
  keysList: vi.fn(() => Promise.resolve([])),
}))
vi.mock('./api', async () => {
  const models = await import('../../wailsjs/go/models')
  return {
    GetSettings: getSettings,
    KeysList: keysList,
    VaultState: vaultState,
    errorText: String,
    model: models.model,
  }
})

import { refreshAll, refreshSettings, settings, vault } from './stores.svelte'

describe('stores', () => {
  it('settings changes notify subscribers after refreshSettings', async () => {
    const seen: number[] = []
    const stop = $effect.root(() => {
      $effect(() => {
        seen.push(settings.terminal_size)
      })
    })
    await Promise.resolve()
    const model = (await import('../../wailsjs/go/models')).model
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
})
