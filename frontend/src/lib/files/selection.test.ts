import { describe, expect, it } from 'vitest'
import { clickSelect, emptySelection, keySelect, pruneSelection, selectAll } from './selection'

const order = ['a', 'b', 'c', 'd', 'e']

describe('selection', () => {
  it('a click selects one, Ctrl toggles, Shift selects a range from the anchor', () => {
    let s = clickSelect(emptySelection(), order, 'b', {})
    expect(s.selected).toEqual(['b'])
    s = clickSelect(s, order, 'd', { ctrl: true })
    expect(s.selected).toEqual(['b', 'd'])
    s = clickSelect(s, order, 'b', { ctrl: true })
    expect(s.selected).toEqual(['d'])
    // As in Explorer, a Ctrl+click moves the anchor even when it deselects.
    s = clickSelect(s, order, 'a', { shift: true })
    expect(s.selected).toEqual(['a', 'b'])
    expect(s.anchor).toBe('b')
    s = clickSelect(s, order, 'e', { shift: true })
    expect(s.selected).toEqual(['b', 'c', 'd', 'e'])
  })

  it('Ctrl+Shift adds a range to the selection', () => {
    let s = clickSelect(emptySelection(), order, 'a', {})
    s = clickSelect(s, order, 'c', { ctrl: true })
    s = clickSelect(s, order, 'e', { ctrl: true, shift: true })
    expect(s.selected.sort()).toEqual(['a', 'c', 'd', 'e'])
  })

  it('arrows, Home and End move the cursor; Shift extends', () => {
    let s = keySelect(emptySelection(), order, 'ArrowDown', {})
    expect(s).toEqual({ selected: ['a'], anchor: 'a', cursor: 'a' })
    s = keySelect(s, order, 'ArrowDown', { shift: true })
    s = keySelect(s, order, 'ArrowDown', { shift: true })
    expect(s.selected).toEqual(['a', 'b', 'c'])
    s = keySelect(s, order, 'ArrowUp', { shift: true })
    expect(s.selected).toEqual(['a', 'b'])
    s = keySelect(s, order, 'End', {})
    expect(s.selected).toEqual(['e'])
    s = keySelect(s, order, 'Home', { shift: true })
    expect(s.selected).toEqual(order)
    s = keySelect(s, order, 'ArrowUp', {})
    expect(s.cursor).toBe('a')
    s = keySelect(s, order, 'ArrowDown', { ctrl: true })
    expect(s).toEqual({ selected: ['a'], anchor: 'a', cursor: 'b' })
  })

  it('Ctrl+A selects all; a refresh keeps only names still there', () => {
    const s = selectAll(order)
    expect(s.selected).toEqual(order)
    const p = pruneSelection({ selected: ['a', 'x'], anchor: 'x', cursor: 'a' }, order)
    expect(p).toEqual({ selected: ['a'], anchor: '', cursor: 'a' })
  })
})
