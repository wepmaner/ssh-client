import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ KeyGenerate: vi.fn() }))
vi.mock('../api', () => ({ ...api, errorText: String }))

import KeyCreateDialog from './KeyCreateDialog.svelte'

const key = { id: 'k1', name: 'work' }

function setup() {
  const oncreated = vi.fn()
  render(KeyCreateDialog, { open: true, onclose: () => {}, oncreated })
  return {
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
})
