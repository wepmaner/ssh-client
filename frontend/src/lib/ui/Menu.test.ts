import { fireEvent, render, screen } from '@testing-library/svelte'
import { afterEach, describe, expect, it } from 'vitest'

import Menu from './Menu.svelte'

describe('Menu', () => {
  let anchor: HTMLButtonElement
  afterEach(() => anchor?.remove())

  it('focuses the anchor before the item action runs', async () => {
    anchor = document.createElement('button')
    anchor.textContent = 'Ещё'
    document.body.append(anchor)
    let focusedOnSelect: Element | null = null
    // `anchor` is also a Svelte mount option, so the props go under `props`.
    render(Menu, {
      props: {
        anchor,
        onclose: () => {},
        items: [{ label: 'Изменить', onselect: () => (focusedOnSelect = document.activeElement) }],
      },
    })
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Изменить' }))
    expect(focusedOnSelect).toBe(anchor)
  })

  it('shows a shortcut hint muted on the right, out of the item name', async () => {
    anchor = document.createElement('button')
    document.body.append(anchor)
    render(Menu, {
      props: {
        anchor,
        onclose: () => {},
        items: [
          { label: 'Копировать', hint: 'Ctrl+C', onselect: () => {} },
          { label: 'Открыть', onselect: () => {} },
        ],
      },
    })
    const copy = screen.getByRole('menuitem', { name: 'Копировать' })
    expect(copy.querySelector('.hint')?.textContent).toBe('Ctrl+C')
    expect(copy.getAttribute('aria-keyshortcuts')).toBe('Ctrl+C')
    const open = screen.getByRole('menuitem', { name: 'Открыть' })
    expect(open.querySelector('.hint')).toBeNull()
    expect(open.textContent?.trim()).toBe('Открыть')
  })
})
