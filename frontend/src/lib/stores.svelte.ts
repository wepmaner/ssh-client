import { GetSettings, GroupsList, HostsList, KeysList, VaultState, app, errorText, model } from './api'

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

export type View = 'hosts' | 'keys' | 'forwards' | 'settings'

/**
 * view is the section of the home tab; activeTab says whether the home tab or the selected
 * session tab is in front. editHostId asks the hosts screen to open that host in its dialog.
 */
export const ui = $state<{ view: View; activeTab: 'home' | 'session'; editHostId: string }>({
  view: 'hosts',
  activeTab: 'home',
  editHostId: '',
})

export const keys = $state<app.KeyDTO[]>([])

/** Saved hosts and their groups, sorted by name in Go. */
export const hosts = $state<app.HostDTO[]>([])
export const groups = $state<app.GroupDTO[]>([])

/** Reloads hosts and groups together: group counts depend on the hosts. */
export async function refreshHosts(): Promise<void> {
  const [h, g] = await Promise.all([HostsList(), GroupsList()])
  hosts.splice(0, hosts.length, ...plain(h))
  groups.splice(0, groups.length, ...plain(g))
}

export const toastState = $state({ message: '' })
let toastTimer: ReturnType<typeof setTimeout> | undefined

/** Shows a small bottom-centre plaque for 2 seconds. */
export function toast(message: string): void {
  clearTimeout(toastTimer)
  toastState.message = message
  toastTimer = setTimeout(() => (toastState.message = ''), 2000)
}

export async function refreshKeys(): Promise<void> {
  keys.splice(0, keys.length, ...plain(await KeysList()))
}

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
    if (!vault.locked && !vault.corrupt) {
      await refreshSettings()
      await refreshKeys()
      await refreshHosts()
    }
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
