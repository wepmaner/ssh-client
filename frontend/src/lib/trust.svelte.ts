// Questions about unknown server keys. Go sends trust:ask when a session or a forward meets a key
// it has never seen and waits for TrustAnswer; trust:cancel withdraws a question (tab closed,
// forward stopped). The dialog shows the first queued question, the rest wait their turn.
import { tick } from 'svelte'
import { EventsOn, TrustAnswer, errorText } from './api'
import { focusTerminal, sessions } from './sessions.svelte'
import { toast, ui } from './stores.svelte'

/** Payload of trust:ask, see app.TrustAskDTO. */
export interface TrustAsk {
  id: string
  session_id: string
  forward_host_id: string
  /** Set when the question belongs to the MCP login of this host; it shows whatever tab is active. */
  mcp_host_id?: string
  host: string
  port: number
  user: string
  key_type: string
  fingerprint: string
}

export const trust = $state<{ queue: TrustAsk[] }>({ queue: [] })

/** Title of the trust dialog, also its accessible name. */
export const trustTitle = 'Неизвестный сервер'

/** host:port with an IPv6 host in brackets, as Go's net.JoinHostPort writes it. */
export function hostPort(host: string, port: number): string {
  return (host.includes(':') ? `[${host}]` : host) + ':' + port
}

/** What had focus before the dialog opened; it gets focus back once the queue is empty. */
let returnFocus: Element | null = null
/** Questions whose answer is on its way to Go, and those of them withdrawn meanwhile. */
const answering = new Set<string>()
const withdrawn = new Set<string>()

/** Hands focus back after the last question closed: to the terminal of the active session it
 *  asked for (the password prompt follows there), otherwise to what had focus before. */
async function restoreFocus(q: TrustAsk): Promise<void> {
  await tick() // the dialog has unmounted
  if (trust.queue.length > 0) return // the next question has the focus now
  const prev = returnFocus
  returnFocus = null
  const activeSession = ui.activeTab === 'session' && sessions.activeId === q.session_id
  if (q.session_id && activeSession && focusTerminal(q.session_id)) return
  if (prev instanceof HTMLElement && prev.isConnected && prev !== document.body) prev.focus()
}

function remove(id: string): TrustAsk | undefined {
  const i = trust.queue.findIndex((q) => q.id === id)
  if (i < 0) return undefined
  const [q] = trust.queue.splice(i, 1)
  if (trust.queue.length === 0) void restoreFocus(q)
  return q
}

/** Listens to trust:ask and trust:cancel; returns the unsubscribe function. */
export function subscribeTrust(): () => void {
  const offAsk = EventsOn('trust:ask', (q: TrustAsk) => {
    if (trust.queue.some((x) => x.id === q.id)) return
    // Remember what has focus, unless it is the trust dialog that is just closing.
    const active = document.activeElement
    if (trust.queue.length === 0 && !active?.closest(`[role="dialog"][aria-label="${trustTitle}"]`)) {
      returnFocus = active
    }
    trust.queue.push(q)
  })
  const offCancel = EventsOn('trust:cancel', (p: { id: string }) => {
    if (answering.has(p.id)) withdrawn.add(p.id)
    remove(p.id)
  })
  return () => {
    offAsk()
    offCancel()
  }
}

/** Answers question id once; a question already answered or withdrawn is left alone. */
export async function answerTrust(id: string, ok: boolean): Promise<void> {
  if (!remove(id)) return
  answering.add(id)
  try {
    await TrustAnswer(id, ok)
  } catch (e) {
    // Withdrawn while the answer was on its way: the answer changes nothing, no need to say so.
    if (!withdrawn.has(id)) toast(errorText(e))
  } finally {
    answering.delete(id)
    withdrawn.delete(id)
  }
}

/** Whether the forwards of the host wait for a trust answer: their mini-terminal is not needed then. */
export function hostAsksTrust(hostId: string): boolean {
  return trust.queue.some((q) => q.forward_host_id === hostId)
}
