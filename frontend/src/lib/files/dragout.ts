// Dragging server rows out of the window into Explorer. The row drag is an HTML drag (it moves rows
// between folders inside the list). Go watches it from its start: when it leaves the window with the
// button held, Go ends it and starts a system drag of virtual files that Explorer copies with its
// own progress (FilesDragOut). The window's drop target tells Go when the drag left.
import { errorText, FilesDragOut } from '../api'
import { toast } from '../stores.svelte'

/**
 * Hands the row drag of paths in tab tabId to Go as it starts. The call returns when the drag ends,
 * inside the window or in Explorer; its error (too many files, a lost connection) is shown then.
 */
export function watchDragOut(
  tabId: string,
  paths: string[],
  dragOut: (tabId: string, paths: string[]) => Promise<void> = FilesDragOut,
): Promise<void> {
  return dragOut(tabId, paths).catch((err: unknown) => toast(errorText(err)))
}
