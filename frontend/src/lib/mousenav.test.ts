import { describe, expect, it } from 'vitest'
import { blockMouseNavigation, sideButton } from './mousenav'

describe('mouse side buttons', () => {
  it('names button 3 back and button 4 forward', () => {
    expect(sideButton(new MouseEvent('mouseup', { button: 3 }))).toBe('back')
    expect(sideButton(new MouseEvent('mouseup', { button: 4 }))).toBe('forward')
    expect(sideButton(new MouseEvent('mouseup', { button: 0 }))).toBeNull()
    expect(sideButton(new MouseEvent('mouseup', { button: 2 }))).toBeNull()
  })

  it('cancels the page navigation of the side buttons only, and still delivers the events', () => {
    const off = blockMouseNavigation(window)
    let seen = 0
    const onup = () => seen++
    document.body.addEventListener('mouseup', onup)
    for (const type of ['mousedown', 'mouseup', 'auxclick']) {
      const side = new MouseEvent(type, { button: 3, bubbles: true, cancelable: true })
      document.body.dispatchEvent(side)
      expect(side.defaultPrevented).toBe(true)
    }
    const fwd = new MouseEvent('mouseup', { button: 4, bubbles: true, cancelable: true })
    document.body.dispatchEvent(fwd)
    expect(fwd.defaultPrevented).toBe(true)
    const left = new MouseEvent('mouseup', { button: 0, bubbles: true, cancelable: true })
    document.body.dispatchEvent(left)
    expect(left.defaultPrevented).toBe(false)
    expect(seen).toBe(3)
    off()
    const after = new MouseEvent('mouseup', { button: 3, bubbles: true, cancelable: true })
    document.body.dispatchEvent(after)
    expect(after.defaultPrevented).toBe(false)
    document.body.removeEventListener('mouseup', onup)
  })
})
