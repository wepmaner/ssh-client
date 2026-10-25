import { render, screen } from '@testing-library/svelte'
import { createRawSnippet, tick } from 'svelte'
import { afterEach, describe, expect, it } from 'vitest'

import Dialog from './Dialog.svelte'

const children = createRawSnippet(() => ({ render: () => '<input aria-label="Поле" />' }))

describe('Dialog', () => {
  let opener: HTMLButtonElement
  afterEach(() => opener?.remove())

  function focusOpener(): void {
    opener = document.createElement('button')
    opener.textContent = 'Открыть'
    document.body.append(opener)
    opener.focus()
  }

  it('focuses the first field and gives focus back to the opener on close', async () => {
    focusOpener()
    const { rerender } = render(Dialog, { open: true, title: 'Тест', onclose: () => {}, children })
    await tick()
    expect(document.activeElement).toBe(screen.getByLabelText('Поле'))
    await rerender({ open: false })
    await tick()
    expect(document.activeElement).toBe(opener)
  })

  it('does not focus an opener that has left the page', async () => {
    focusOpener()
    const { rerender } = render(Dialog, { open: true, title: 'Тест', onclose: () => {}, children })
    await tick()
    opener.remove()
    await rerender({ open: false })
    await tick()
    expect(document.activeElement).toBe(document.body)
  })

  it('leaves the focus alone when the closing code moved it elsewhere', async () => {
    focusOpener()
    const other = document.createElement('button')
    document.body.append(other)
    try {
      const { rerender } = render(Dialog, { open: true, title: 'Тест', onclose: () => {}, children })
      await tick()
      other.focus()
      await rerender({ open: false })
      await tick()
      expect(document.activeElement).toBe(other)
    } finally {
      other.remove()
    }
  })

  it('restoreFocus={false} leaves the focus to the caller', async () => {
    focusOpener()
    const { rerender } = render(Dialog, { open: true, title: 'Тест', onclose: () => {}, children, restoreFocus: false })
    await tick()
    await rerender({ open: false })
    await tick()
    expect(document.activeElement).not.toBe(opener)
  })
})
