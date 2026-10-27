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
})
