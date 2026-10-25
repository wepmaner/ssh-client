// Idle lock: after a set time without keyboard, mouse, wheel or pointer input the callback runs.
// Input only stamps the time (listeners are passive, in the capture phase so the terminal cannot
// swallow them; pointer movement stamps at most once a second); a single timer checks the stamp
// when the time is up and re-arms for the rest. A callback that rejects re-arms the countdown.

const activityEvents = ['keydown', 'mousedown', 'wheel'] as const
const moveThrottleMs = 1000

export interface IdleLock {
  /** (Re)starts the countdown with a limit in ms; 0 or less switches the lock off. */
  start(limitMs: number): void
  stop(): void
}

export function createIdleLock(onidle: () => void | Promise<void>, target: EventTarget = window): IdleLock {
  let limit = 0
  let last = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  const activity = () => {
    last = Date.now()
  }
  const move = () => {
    const now = Date.now()
    if (now - last >= moveThrottleMs) last = now
  }

  function check(): void {
    const idle = Date.now() - last
    if (idle < limit) {
      timer = setTimeout(check, limit - idle)
      return
    }
    const again = limit
    stop()
    // The lock did not happen (the error is the callback's to show): count down again.
    Promise.resolve()
      .then(onidle)
      .catch(() => start(again))
  }

  function stop(): void {
    clearTimeout(timer)
    timer = undefined
    for (const type of activityEvents) target.removeEventListener(type, activity, { capture: true })
    target.removeEventListener('pointermove', move, { capture: true })
  }

  function start(limitMs: number): void {
    stop()
    if (limitMs <= 0) return
    limit = limitMs
    last = Date.now()
    for (const type of activityEvents) target.addEventListener(type, activity, { capture: true, passive: true })
    target.addEventListener('pointermove', move, { capture: true, passive: true })
    timer = setTimeout(check, limit)
  }

  return { start, stop }
}
