// After a restore Go has closed every session and forward and swapped the vault: the UI drops its
// session tabs and loads everything again, as on startup.
import { refreshForwards } from '../forwards/forwards.svelte'
import { closeSession, sessions } from '../sessions.svelte'
import { refreshAll, toast, vault } from '../stores.svelte'

export async function afterRestore(): Promise<void> {
  for (const tab of [...sessions.list]) closeSession(tab.id)
  await refreshAll()
  if (!vault.locked && !vault.corrupt) await refreshForwards().catch(() => {})
  toast('Хранилище восстановлено из копии')
}
