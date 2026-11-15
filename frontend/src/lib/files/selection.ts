// Explorer-style selection over the visible order of names: click, Ctrl, Shift, arrows.

export interface Selection {
  /** Selected names. */
  selected: string[]
  /** Where a Shift range starts. */
  anchor: string
  /** The row with the keyboard cursor. */
  cursor: string
}

export const emptySelection = (): Selection => ({ selected: [], anchor: '', cursor: '' })

function range(order: string[], a: string, b: string): string[] {
  let i = order.indexOf(a)
  const j = order.indexOf(b)
  if (j < 0) return []
  if (i < 0) i = j
  const [lo, hi] = i <= j ? [i, j] : [j, i]
  return order.slice(lo, hi + 1)
}

/** A mouse click on name with the modifiers held. */
export function clickSelect(s: Selection, order: string[], name: string, mods: { ctrl?: boolean; shift?: boolean }): Selection {
  if (mods.shift) {
    const anchor = s.anchor && order.includes(s.anchor) ? s.anchor : name
    const r = range(order, anchor, name)
    const selected = mods.ctrl ? [...new Set([...s.selected, ...r])] : r
    return { selected, anchor, cursor: name }
  }
  if (mods.ctrl) {
    const on = s.selected.includes(name)
    return { selected: on ? s.selected.filter((n) => n !== name) : [...s.selected, name], anchor: name, cursor: name }
  }
  return { selected: [name], anchor: name, cursor: name }
}

export type NavKey = 'ArrowUp' | 'ArrowDown' | 'Home' | 'End' | 'PageUp' | 'PageDown'

/** Moves the cursor; Shift extends from the anchor, Ctrl moves the cursor only. */
export function keySelect(s: Selection, order: string[], key: NavKey, mods: { ctrl?: boolean; shift?: boolean }, page = 10): Selection {
  if (order.length === 0) return s
  const cur = order.indexOf(s.cursor)
  let i: number
  if (key === 'Home') i = 0
  else if (key === 'End') i = order.length - 1
  else if (cur < 0) i = key === 'ArrowUp' || key === 'PageUp' ? order.length - 1 : 0
  else {
    const step = key === 'ArrowUp' ? -1 : key === 'ArrowDown' ? 1 : key === 'PageUp' ? -page : page
    i = Math.max(0, Math.min(order.length - 1, cur + step))
  }
  const name = order[i]
  if (mods.shift) {
    const anchor = s.anchor && order.includes(s.anchor) ? s.anchor : (s.cursor && order.includes(s.cursor) ? s.cursor : name)
    return { selected: range(order, anchor, name), anchor, cursor: name }
  }
  if (mods.ctrl) return { ...s, cursor: name }
  return { selected: [name], anchor: name, cursor: name }
}

export function selectAll(order: string[]): Selection {
  return { selected: [...order], anchor: order[0] ?? '', cursor: order[order.length - 1] ?? '' }
}

/** Keeps only names still visible, e.g. after a refresh or a filter. */
export function pruneSelection(s: Selection, order: string[]): Selection {
  const keep = new Set(order)
  return {
    selected: s.selected.filter((n) => keep.has(n)),
    anchor: keep.has(s.anchor) ? s.anchor : '',
    cursor: keep.has(s.cursor) ? s.cursor : '',
  }
}
