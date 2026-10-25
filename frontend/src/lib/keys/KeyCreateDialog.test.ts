import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ KeyGenerate: vi.fn() }))
vi.mock('../api', () => ({ ...api, errorText: String }))

import KeyCreateDialog from './KeyCreateDialog.svelte'

const key = { id: 'k1', name: 'work' }

function setup() {
  const oncreated = vi.fn()
  const onclose = vi.fn()
  const view = render(KeyCreateDialog, { open: true, onclose, oncreated })
  return {
    view,
    onclose,
    oncreated,
    name: screen.getByLabelText('Название'),
    phrase: screen.getByLabelText('Парольная фраза (необязательно)'),
    repeat: screen.getByLabelText('Повторите фразу'),
    submit: screen.getByRole('button', { name: 'Создать' }) as HTMLButtonElement,
  }
}

describe('KeyCreateDialog', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.KeyGenerate.mockResolvedValue(key)
  })

  it('creates an unencrypted key when the passphrase is empty', async () => {
    const f = setup()
    await fireEvent.input(f.name, { target: { value: 'work' } })
    await fireEvent.click(f.submit)
    await vi.waitFor(() => expect(f.oncreated).toHaveBeenCalledWith(key))
    expect(api.KeyGenerate).toHaveBeenCalledWith('work', 'ed25519', '', '')
  })

  it('passes a confirmed passphrase', async () => {
    const f = setup()
    await fireEvent.input(f.name, { target: { value: 'work' } })
    await fireEvent.input(f.phrase, { target: { value: 'secret phrase' } })
    await fireEvent.input(f.repeat, { target: { value: 'secret phrase' } })
    await fireEvent.click(f.submit)
    await vi.waitFor(() => expect(api.KeyGenerate).toHaveBeenCalledWith('work', 'ed25519', '', 'secret phrase'))
  })

  it('shows a mismatch inline and blocks creation', async () => {
    const f = setup()
    await fireEvent.input(f.name, { target: { value: 'work' } })
    await fireEvent.input(f.phrase, { target: { value: 'one' } })
    await fireEvent.input(f.repeat, { target: { value: 'two' } })
    expect(screen.getByText('Фразы не совпадают')).toBeTruthy()
    expect(f.submit.disabled).toBe(true)
    await fireEvent.input(f.repeat, { target: { value: 'one' } })
    expect(screen.queryByText('Фразы не совпадают')).toBeNull()
    expect(f.submit.disabled).toBe(false)
  })

  it('asks to repeat a typed passphrase instead of silently blocking', async () => {
    const f = setup()
    await fireEvent.input(f.name, { target: { value: 'work' } })
    await fireEvent.input(f.phrase, { target: { value: 'one' } })
    expect(screen.getByText('Повторите парольную фразу')).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(f.submit.disabled).toBe(true)
    await fireEvent.input(f.repeat, { target: { value: 'one' } })
    expect(screen.queryByText('Повторите парольную фразу')).toBeNull()
  })

  for (const how of ['cancel', 'escape'] as const) {
    it(`forgets the passphrase on close (${how})`, async () => {
      const f = setup()
      await fireEvent.input(f.phrase, { target: { value: 'secret' } })
      await fireEvent.input(f.repeat, { target: { value: 'secret' } })
      if (how === 'cancel') await fireEvent.click(screen.getByRole('button', { name: 'Отмена' }))
      else await fireEvent.keyDown(window, { key: 'Escape' })
      expect(f.onclose).toHaveBeenCalled()
      // The parent has not closed the dialog yet: the fields must already be empty.
      expect((screen.getByLabelText('Парольная фраза (необязательно)') as HTMLInputElement).value).toBe('')
      expect((screen.getByLabelText('Повторите фразу') as HTMLInputElement).value).toBe('')
      await f.view.rerender({ open: false })
      await f.view.rerender({ open: true })
      expect((screen.getByLabelText('Парольная фраза (необязательно)') as HTMLInputElement).value).toBe('')
      expect((screen.getByLabelText('Повторите фразу') as HTMLInputElement).value).toBe('')
    })
  }
})
