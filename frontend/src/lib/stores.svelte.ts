import { GetSettings, VaultState, model } from './api'

export const vault = $state({
  ready: false,
  locked: false,
  has_password: false,
  corrupt: false,
  error: '',
})

export const settings = $state<model.Settings>(model.Settings.createFrom({}))

export async function refreshVault(): Promise<void> {
  Object.assign(vault, await VaultState(), { ready: true })
}

export async function refreshSettings(): Promise<void> {
  Object.assign(settings, await GetSettings())
}

export async function refreshAll(): Promise<void> {
  await refreshVault()
  if (!vault.locked && !vault.corrupt) await refreshSettings()
}
