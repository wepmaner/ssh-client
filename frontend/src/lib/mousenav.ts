/** The side buttons of a mouse: 3 is «back», 4 is «forward», as in a browser. */
export function sideButton(e: MouseEvent): 'back' | 'forward' | null {
  return e.button === 3 ? 'back' : e.button === 4 ? 'forward' : null
}

/**
 * Stops the side buttons of a mouse from navigating the page: WebView2 would go back in its own
 * history and leave the app. The views still get the events and use them for their own history.
 * pointerdown is left alone: cancelling it would suppress the mousedown and mouseup that follow.
 */
export function blockMouseNavigation(target: Window = window): () => void {
  const stop = (e: MouseEvent) => {
    if (sideButton(e)) e.preventDefault()
  }
  const types = ['mousedown', 'mouseup', 'auxclick', 'pointerup'] as const
  for (const t of types) target.addEventListener(t, stop, true)
  return () => {
    for (const t of types) target.removeEventListener(t, stop, true)
  }
}
