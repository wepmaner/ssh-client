import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import Select from './Select.svelte'

const options = [
  { value: 'a', label: 'Альфа' },
  { value: 'b', label: 'Бета' },
  { value: 'c', label: 'Гамма' },
]

function setup(value = 'b', disabled = false) {
  const onchange = vi.fn()
  render(Select, { value, options, onchange, disabled, 'aria-label': 'Поле' })
  return { field: screen.getByRole('combobox', { name: 'Поле' }), onchange }
}

const list = () => screen.queryByRole('listbox')

describe('Select', () => {
  it('shows the label of the value', () => {
    expect(setup().field.textContent?.trim()).toBe('Бета')
  })

  it('opens on a click and marks the current option', async () => {
    const { field } = setup()
    await fireEvent.click(field)
    expect(field.getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByRole('option', { selected: true }).textContent?.trim()).toBe('Бета')
    expect(field.getAttribute('aria-activedescendant')).toBe(screen.getByRole('option', { selected: true }).id)
  })

  it('reports a pressed option and closes', async () => {
    const { field, onchange } = setup()
    await fireEvent.click(field)
    await fireEvent.click(screen.getByRole('option', { name: 'Гамма' }))
    expect(onchange).toHaveBeenCalledWith('c')
    expect(list()).toBeNull()
  })

  it('stays on the value the caller holds', async () => {
    const { field } = setup()
    await fireEvent.click(field)
    await fireEvent.click(screen.getByRole('option', { name: 'Гамма' }))
    expect(field.dataset.value).toBe('b')
  })

  it('does not report the current option again', async () => {
    const { field, onchange } = setup()
    await fireEvent.click(field)
    await fireEvent.click(screen.getByRole('option', { name: 'Бета' }))
    expect(onchange).not.toHaveBeenCalled()
  })

  it('works from the keyboard', async () => {
    const { field, onchange } = setup()
    await fireEvent.keyDown(field, { key: 'ArrowDown' })
    expect(list()).toBeTruthy()
    await fireEvent.keyDown(field, { key: 'ArrowDown' })
    await fireEvent.keyDown(field, { key: 'Enter' })
    expect(onchange).toHaveBeenCalledWith('c')
    expect(list()).toBeNull()
  })

  it('stops at the ends of the list', async () => {
    const { field, onchange } = setup()
    await fireEvent.keyDown(field, { key: ' ' })
    await fireEvent.keyDown(field, { key: 'Home' })
    await fireEvent.keyDown(field, { key: 'ArrowUp' })
    await fireEvent.keyDown(field, { key: ' ' })
    expect(onchange).toHaveBeenCalledWith('a')
  })

  it('Esc closes only the list, not the dialog around it', async () => {
    const { field, onchange } = setup()
    const around = vi.fn()
    window.addEventListener('keydown', around)
    await fireEvent.click(field)
    await fireEvent.keyDown(field, { key: 'Escape' })
    expect(list()).toBeNull()
    expect(onchange).not.toHaveBeenCalled()
    expect(around).not.toHaveBeenCalled()
    await fireEvent.keyDown(field, { key: 'Escape' })
    expect(around).toHaveBeenCalledOnce()
    window.removeEventListener('keydown', around)
  })

  it('closes when focus leaves', async () => {
    const { field } = setup()
    await fireEvent.click(field)
    await fireEvent.blur(field)
    expect(list()).toBeNull()
  })

  it('keeps focus on the field when an option is pressed', async () => {
    const { field } = setup()
    await fireEvent.click(field)
    const notPrevented = await fireEvent.mouseDown(screen.getByRole('option', { name: 'Гамма' }))
    expect(notPrevented).toBe(false)
  })

  it('does not open while disabled', async () => {
    const { field } = setup('b', true)
    await fireEvent.keyDown(field, { key: 'ArrowDown' })
    expect(list()).toBeNull()
  })
})
