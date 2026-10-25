import { beforeEach, describe, expect, it, vi } from 'vitest'

type Handler = (data: unknown) => void
const listeners = vi.hoisted(() => new Map<string, Handler>())
vi.mock('./api', () => ({
  EventsOn: (name: string, cb: Handler) => {
    listeners.set(name, cb)
    return () => listeners.delete(name)
  },
}))

import { MENU_ACTION_EVENT, onMenuAction } from './menus'

describe('onMenuAction', () => {
  beforeEach(() => listeners.clear())

  it('routes clicks of its menu to the action handler with the element id', () => {
    const edit = vi.fn()
    const del = vi.fn()
    onMenuAction('host', { edit, delete: del })
    listeners.get(MENU_ACTION_EVENT)?.({ menu: 'host', action: 'delete', id: 'h1' })
    expect(del).toHaveBeenCalledWith('h1')
    expect(edit).not.toHaveBeenCalled()
  })

  it('ignores other menus, unknown actions and clicks without an id', () => {
    const edit = vi.fn()
    onMenuAction('host', { edit })
    const emit = listeners.get(MENU_ACTION_EVENT)!
    emit({ menu: 'key', action: 'edit', id: 'k1' })
    emit({ menu: 'host', action: 'rename', id: 'h1' })
    emit({ menu: 'host', action: 'edit', id: '' })
    emit(null)
    expect(edit).not.toHaveBeenCalled()
  })

  it('returns the unsubscribe function', () => {
    const off = onMenuAction('key', {})
    off()
    expect(listeners.has(MENU_ACTION_EVENT)).toBe(false)
  })
})
