// Dragging server rows out of the window into Explorer. The row drag is an HTML drag (it moves rows
// between folders inside the list); once it leaves the window with the button held, Go ends it and
// starts a system drag of virtual files that Explorer copies with its own progress (FilesDragOut).
import { errorText, FilesDragOut } from '../api'
import { toast } from '../stores.svelte'

/**
 * How long the drag may stay outside the document before it counts as out of the window: leaving one
 * element for another inside fires dragleave too, followed at once by dragenter or dragover.
 */
export const LEAVE_DELAY = 60

/**
 * Watches the row drag of paths in tab tabId until it ends (dragend or drop); when it leaves the
 * window, hands it to Go. Go does nothing when the cursor is back over the window or the button is
 * up, so the watch goes on until the drag ends. Returns the function that stops watching.
 */
export function watchDragOut(
  tabId: string,
  paths: string[],
  doc: Document = document,
  dragOut?: (tabId: string, paths: string[]) => Promise<void>,
): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined
  let busy = false
  const clear = () => {
    if (timer !== undefined) clearTimeout(timer)
    timer = undefined
  }
  const onleave = (e: DragEvent) => {
    if (e.relatedTarget !== null || busy) return
    clear()
    timer = setTimeout(() => {
      timer = undefined
      busy = true
      ;(dragOut ?? FilesDragOut)(tabId, paths)
        .catch((err: unknown) => toast(errorText(err)))
        .finally(() => (busy = false))
    }, LEAVE_DELAY)
  }
  const stop = () => {
    clear()
    doc.removeEventListener('dragleave', onleave, true)
    doc.removeEventListener('dragenter', clear, true)
    doc.removeEventListener('dragover', clear, true)
    doc.removeEventListener('dragend', stop, true)
    doc.removeEventListener('drop', stop, true)
  }
  doc.addEventListener('dragleave', onleave, true)
  doc.addEventListener('dragenter', clear, true)
  doc.addEventListener('dragover', clear, true)
  doc.addEventListener('dragend', stop, true)
  doc.addEventListener('drop', stop, true)
  return stop
}
