import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  VaultCounts: vi.fn(() => Promise.resolve({ keys: 0, hosts: 0, forwards: 0 })),
  VaultRestore: vi.fn(() => Promise.resolve()),
}))
vi.mock('../api', () => ({ ...api, errorText: String }))

import RestoreDialog from './RestoreDialog.svelte'

const path = 'D:\\backups\\sshclient-backup-2026-10-27.vault'
const warning = /Текущие ключи, хосты и пробросы будут заменены копией/

describe('RestoreDialog', () => {
  beforeEach(() => vi.clearAllMocks())

  it('restores into an empty vault without the replace warning', async () => {
    const onrestored = vi.fn()
    const onclose = vi.fn()
    render(RestoreDialog, { path, onclose, onrestored })
    expect(screen.getByText('sshclient-backup-2026-10-27.vault')).toBeTruthy()
    await waitFor(() => expect(api.VaultCounts).toHaveBeenCalled())
    expect(screen.queryByText(warning)).toBeNull()
    const submit = screen.getByRole('button', { name: 'Восстановить' }) as HTMLButtonElement
    expect(submit.disabled).toBe(true)
    const field = screen.getByLabelText('Пароль копии')
    await fireEvent.input(field, { target: { value: 'backup pass' } })
    await fireEvent.submit(field.closest('form')!) // Enter in the field
    expect(api.VaultRestore).toHaveBeenCalledWith(path, 'backup pass')
    await waitFor(() => expect(onrestored).toHaveBeenCalled())
    expect(onclose).toHaveBeenCalled()
  })

  it('warns and offers «Заменить» when the vault has data', async () => {
    api.VaultCounts.mockResolvedValueOnce({ keys: 0, hosts: 0, forwards: 1 })
    render(RestoreDialog, { path, onclose: () => {}, onrestored: () => {} })
    expect(await screen.findByText(warning)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Заменить' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Восстановить' })).toBeNull()
  })

  it('shows a wrong password under the field and stays open', async () => {
    api.VaultRestore.mockRejectedValueOnce('field:password: Неверный пароль копии')
    const onclose = vi.fn()
    const onrestored = vi.fn()
    render(RestoreDialog, { path, onclose, onrestored })
    const field = screen.getByLabelText('Пароль копии')
    await fireEvent.input(field, { target: { value: 'wrong' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Восстановить' }))
    const err = await screen.findByText('Неверный пароль копии')
    expect(field.getAttribute('aria-describedby')).toBe(err.id)
    expect(onclose).not.toHaveBeenCalled()
    expect(onrestored).not.toHaveBeenCalled()
  })

  it('stays closed without a file and closes on Escape', async () => {
    const onclose = vi.fn()
    const { rerender } = render(RestoreDialog, { path: '', onclose, onrestored: () => {} })
    expect(screen.queryByLabelText('Пароль копии')).toBeNull()
    await rerender({ path, onclose, onrestored: () => {} })
    expect(screen.getByLabelText('Пароль копии')).toBeTruthy()
    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(onclose).toHaveBeenCalled()
  })
})
