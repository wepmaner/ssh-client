import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../api', () => ({
  VaultCounts: () => Promise.resolve({ keys: 2, hosts: 5 }),
  VaultSetPassword: vi.fn(() => Promise.resolve()),
  errorText: String,
}))

import VaultDialog from './VaultDialog.svelte'

describe('VaultDialog', () => {
  it('enables submit only for matching passwords of at least 8 chars', async () => {
    render(VaultDialog, { open: true, onclose: () => {} })
    const first = screen.getByLabelText('Пароль')
    const second = screen.getByLabelText('Ещё раз')
    const submit = screen.getByRole('button', { name: 'Включить шифрование' }) as HTMLButtonElement
    expect(submit.disabled).toBe(true)

    await fireEvent.input(first, { target: { value: 'short' } })
    await fireEvent.input(second, { target: { value: 'short' } })
    expect(submit.disabled).toBe(true)

    await fireEvent.input(first, { target: { value: 'long enough pw' } })
    expect(submit.disabled).toBe(true)
    await fireEvent.input(second, { target: { value: 'long enough pw' } })
    expect(submit.disabled).toBe(false)
  })

  it('shows counts and closes on Escape', async () => {
    const onclose = vi.fn()
    render(VaultDialog, { open: true, onclose })
    expect(await screen.findByText('Будет зашифровано: 2 ключей, 5 хостов')).toBeTruthy()
    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(onclose).toHaveBeenCalled()
  })
})
