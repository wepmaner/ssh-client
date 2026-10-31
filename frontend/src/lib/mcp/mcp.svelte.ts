// Logins of the MCP server that need the owner's input. Go sends mcplogin:open when a login to a
// host asks something and mcplogin:close when it has ended; the dialog shows the first one, the
// logins of other hosts wait their turn.
import { EventsOn, MCPLoginCancel, errorText } from '../api'
import { toast } from '../stores.svelte'

/** Payload of mcplogin:open, see app.MCPLoginOpenDTO. */
export interface McpLogin {
  host_id: string
  login_id: string
  host_name: string
}

export const mcpLogins = $state<{ queue: McpLogin[] }>({ queue: [] })

/** Title of the login dialog, also its accessible name. */
export const mcpLoginTitle = (hostName: string): string => `Вход по MCP: ${hostName}`

function remove(loginId: string): void {
  const i = mcpLogins.queue.findIndex((l) => l.login_id === loginId)
  if (i >= 0) mcpLogins.queue.splice(i, 1)
}

/** Listens to mcplogin:open and mcplogin:close; returns the unsubscribe function. */
export function subscribeMcpLogins(): () => void {
  const offOpen = EventsOn('mcplogin:open', (l: McpLogin) => {
    if (!mcpLogins.queue.some((x) => x.login_id === l.login_id)) mcpLogins.queue.push(l)
  })
  const offClose = EventsOn('mcplogin:close', (p: { host_id: string; login_id: string }) => remove(p.login_id))
  return () => {
    offOpen()
    offClose()
  }
}

/** The owner closed the dialog: the login is cancelled, and its call gets an error. */
export async function cancelMcpLogin(l: McpLogin): Promise<void> {
  remove(l.login_id)
  try {
    await MCPLoginCancel(l.host_id, l.login_id)
  } catch (e) {
    toast(errorText(e))
  }
}
