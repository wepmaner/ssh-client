// Wails runs every bound call in its own goroutine, so two SessionWrite calls in flight may
// reach Go in either order. The sender keeps one call in flight and coalesces keystrokes typed
// meanwhile into the next call, which keeps input in order without delaying the first key.

export type Send = (data: string) => Promise<unknown>

/** Returns a function that queues terminal input for send, in order. Failed sends are reported and dropped. */
export function orderedSender(send: Send, onError: (e: unknown) => void = () => {}): (data: string) => void {
  let pending = ''
  let inFlight = false

  async function pump(): Promise<void> {
    inFlight = true
    while (pending) {
      const chunk = pending
      pending = ''
      try {
        await send(chunk)
      } catch (e) {
        onError(e)
      }
    }
    inFlight = false
  }

  return (data: string) => {
    pending += data
    if (!inFlight) void pump()
  }
}
