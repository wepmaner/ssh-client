import { describe, expect, it, vi } from 'vitest'
import { Events } from '@wailsio/runtime'
import { EventsOn, errorText } from './api'

// Under Vitest @wailsio/runtime is src/test/wails-runtime.ts, whose Emit wraps the payload
// in a WailsEvent as the real runtime does.
const emit = (Events as unknown as { Emit: (name: string, data: unknown) => void }).Emit

describe('EventsOn', () => {
  it('passes WailsEvent.data to the callback, as Wails v2 did', () => {
    const cb = vi.fn()
    const off = EventsOn('term:data:1', cb)
    emit('term:data:1', 'aGk=')
    expect(cb).toHaveBeenCalledWith('aGk=')
    emit('fwd:state:1', { status: 'running' })
    expect(cb).toHaveBeenCalledTimes(1)
    off()
    emit('term:data:1', 'x')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('keeps object payloads as they are', () => {
    const cb = vi.fn()
    const off = EventsOn('menu:action', cb)
    emit('menu:action', { menu: 'host', action: 'edit', id: 'h1' })
    expect(cb).toHaveBeenCalledWith({ menu: 'host', action: 'edit', id: 'h1' })
    off()
  })
})

describe('errorText', () => {
  it('reads the Go error text of a rejected binding call', () => {
    expect(errorText(new Error('Ключ не найден'))).toBe('Ключ не найден')
    expect(errorText('cancelled')).toBe('cancelled')
  })
})
