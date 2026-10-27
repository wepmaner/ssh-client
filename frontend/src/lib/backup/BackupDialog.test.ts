import { fireEvent, render, screen } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ VaultBackup: vi.fn(() => Promise.resolve('D:\b.vault')) }))
vi.mock('../api', () => ({ ...api, errorText: String }))

import BackupDialog from './BackupDialog.svelte'

describe('BackupDialog', () => {
  it('refuses a password shorter than 8 characters or not repeated', async () => {
    render(BackupDialog, { open: true, onclose: () => {}, onsaved: () => {} })
    const first = screen.getByLabelText('Пароль копии')
    const second = screen.getByLabelText('Ещё раз')
    const submit = screen.getByRole('button', { name: 'Сохранить копию' }) as HTMLButtonElement
    await fireEvent.input(first, { target: { value: 'short12' } })
    await fireEvent.input(second, { target: { value: 'short12' } })
    expect(submit.disabled).toBe(true)
    await fireEvent.submit(first.closest('form')!)
    expect(api.VaultBackup).not.toHaveBeenCalled()
    await fireEvent.input(first, { target: { value: 'long enough' } })
    expect(submit.disabled).toBe(true)
    await fireEvent.input(second, { target: { value: 'long enough' } })
    expect(submit.disabled).toBe(false)
  })
})
