import { GetSettings, VaultState, errorText, model } from './api'

export const vault = $state({
  ready: false,
  locked: false,
  has_password: false,
  corrupt: false,
  error: '',
})

// Wails returns class instances; Svelte only deep-proxies plain objects.
const plain = <T>(v: T): T => JSON.parse(JSON.stringify(v))

export const settings = $state<model.Settings>(plain(model.Settings.createFrom({})))

export async function refreshVault(): Promise<void> {
  Object.assign(vault, plain(await VaultState()), { ready: true })
}

export async function refreshSettings(): Promise<void> {
  Object.assign(settings, plain(await GetSettings()))
}

/** Never rejects: a failure becomes a visible vault error state. */
export async function refreshAll(): Promise<void> {
  try {
    await refreshVault()
    if (!vault.locked && !vault.corrupt) await refreshSettings()
  } catch (e) {
    Object.assign(vault, {
      ready: true,
      locked: true,
      has_password: false,
      corrupt: false,
      error: errorText(e),
    })
  }
}
