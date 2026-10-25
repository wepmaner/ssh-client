// Idle lock: after a set time without keyboard, mouse or wheel input the callback runs once.
// Input only stamps the time (listeners are passive, in the capture phase so the terminal cannot
// swallow them); a single timer checks the stamp when the time is up and re-arms for the rest.

const activityEvents = ['keydown', 'mousedown', 'wheel'] as const

export interface IdleLock {
  /** (Re)starts the countdown with a limit in ms; 0 or less switches the lock off. */
  start(limitMs: number): void
  stop(): void
}

export function createIdleLock(onidle: () => void, target: EventTarget = window): IdleLock {
  let limit = 0
  let last = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  const activity = () => {
    last = Date.now()
  }

  function check(): void {
    const idle = Date.now() - last
    if (idle < limit) {
      timer = setTimeout(check, limit - idle)
      return
    }
    stop()
    onidle()
  }

  function stop(): void {
    clearTimeout(timer)
    timer = undefined
    for (const type of activityEvents) target.removeEventListener(type, activity, { capture: true })
  }

  function start(limitMs: number): void {
    stop()
    if (limitMs <= 0) return
    limit = limitMs
    last = Date.now()
    for (const type of activityEvents) target.addEventListener(type, activity, { capture: true, passive: true })
    timer = setTimeout(check, limit)
  }

  return { start, stop }
}
