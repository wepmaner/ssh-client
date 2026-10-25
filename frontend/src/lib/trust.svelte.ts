// Questions about unknown server keys. Go sends trust:ask when a session or a forward meets a key
// it has never seen and waits for TrustAnswer; trust:cancel withdraws a question (tab closed,
// forward stopped). The dialog shows the first queued question, the rest wait their turn.
import { EventsOn, TrustAnswer, errorText } from './api'
import { toast } from './stores.svelte'

/** Payload of trust:ask, see app.TrustAskDTO. */
export interface TrustAsk {
  id: string
  session_id: string
  forward_id: string
  host: string
  port: number
  user: string
  key_type: string
  fingerprint: string
}

export const trust = $state<{ queue: TrustAsk[] }>({ queue: [] })

function remove(id: string): boolean {
  const i = trust.queue.findIndex((q) => q.id === id)
  if (i < 0) return false
  trust.queue.splice(i, 1)
  return true
}

/** Listens to trust:ask and trust:cancel; returns the unsubscribe function. */
export function subscribeTrust(): () => void {
  const offAsk = EventsOn('trust:ask', (q: TrustAsk) => {
    if (!trust.queue.some((x) => x.id === q.id)) trust.queue.push(q)
  })
  const offCancel = EventsOn('trust:cancel', (p: { id: string }) => remove(p.id))
  return () => {
    offAsk()
    offCancel()
  }
}

/** Answers question id once; a question already answered or withdrawn is left alone. */
export async function answerTrust(id: string, ok: boolean): Promise<void> {
  if (!remove(id)) return
  try {
    await TrustAnswer(id, ok)
  } catch (e) {
    toast(errorText(e))
  }
}

/** Whether the forward waits for a trust answer: its mini-terminal is not needed then. */
export function forwardAsksTrust(forwardId: string): boolean {
  return trust.queue.some((q) => q.forward_id === forwardId)
}
