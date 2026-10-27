// After a restore Go has closed every session and forward and swapped the vault: the UI drops its
// session tabs and loads everything again, as on startup.
import { ForwardHostAttach } from '../api'
import { forwards, refreshForwards } from '../forwards/forwards.svelte'
import { closeSession, sessions } from '../sessions.svelte'
import { refreshAll, toast, vault } from '../stores.svelte'

export async function afterRestore(): Promise<void> {
  for (const tab of [...sessions.list]) closeSession(tab.id)
  await refreshAll()
  if (!vault.locked && !vault.corrupt) {
    await refreshForwards().catch(() => {})
    // Go dropped the forwards groups of the old vault; a host kept by the copy has a new group,
    // which holds its output until the UI attaches again.
    for (const h of forwards.hosts) void ForwardHostAttach(h.host_id).catch(() => {})
  }
  toast('Хранилище восстановлено из копии')
}
