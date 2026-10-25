import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  KeyImportFile: vi.fn(),
  KeyImportFileWithPassphrase: vi.fn(),
  KeyImportText: vi.fn(),
  KeyImportTextWithPassphrase: vi.fn(),
}))
vi.mock('../api', () => ({ ...api, errorText: String }))

import KeyImportPanel from './KeyImportPanel.svelte'

const key = { id: 'k1', name: 'a' }

describe('KeyImportPanel', () => {
  beforeEach(() => vi.resetAllMocks())

  it('imports an encrypted key without asking for its passphrase', async () => {
    api.KeyImportFile.mockResolvedValue(key)
    const onimported = vi.fn()
    render(KeyImportPanel, { onimported })
    await fireEvent.click(screen.getByRole('button', { name: 'Выбрать файл…' }))
    await vi.waitFor(() => expect(onimported).toHaveBeenCalledWith(key))
    expect(screen.queryByLabelText('Парольная фраза')).toBeNull()
    expect(api.KeyImportFileWithPassphrase).not.toHaveBeenCalled()
  })

  it('asks the passphrase of a legacy PEM once and retries with it', async () => {
    api.KeyImportFile.mockRejectedValue('passphrase_required:C:\\k.pem')
    api.KeyImportFileWithPassphrase.mockResolvedValue(key)
    const onimported = vi.fn()
    render(KeyImportPanel, { onimported })

    await fireEvent.click(screen.getByRole('button', { name: 'Выбрать файл…' }))
    expect(await screen.findByText(/Старый формат PEM/)).toBeTruthy()
    await fireEvent.input(screen.getByLabelText('Парольная фраза'), { target: { value: 'secret' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Импортировать' }))

    await vi.waitFor(() => expect(onimported).toHaveBeenCalledWith(key))
    expect(api.KeyImportFileWithPassphrase).toHaveBeenCalledWith('C:\\k.pem', 'secret')
  })

  it('ignores a cancelled file dialog silently', async () => {
    api.KeyImportFile.mockRejectedValue('cancelled')
    const onimported = vi.fn()
    render(KeyImportPanel, { onimported })
    await fireEvent.click(screen.getByRole('button', { name: 'Выбрать файл…' }))
    await vi.waitFor(() => expect(api.KeyImportFile).toHaveBeenCalled())
    expect(screen.queryByRole('alert')).toBeNull()
    expect(onimported).not.toHaveBeenCalled()
  })

  it('imports pasted text and shows backend errors inline', async () => {
    api.KeyImportText.mockRejectedValueOnce('Ключ с таким названием уже есть').mockResolvedValueOnce(key)
    const onimported = vi.fn()
    render(KeyImportPanel, { onimported })
    await fireEvent.click(screen.getByRole('tab', { name: 'Вставить текст' }))
    await fireEvent.input(screen.getByLabelText('Приватный ключ'), { target: { value: 'KEY' } })
    const submit = screen.getByRole('button', { name: 'Импортировать' })
    await fireEvent.click(submit)
    expect((await screen.findByRole('alert')).textContent).toBe('Ключ с таким названием уже есть')
    await fireEvent.click(submit)
    await vi.waitFor(() => expect(onimported).toHaveBeenCalledWith(key))
    expect(api.KeyImportText).toHaveBeenLastCalledWith('KEY', '')
  })
})
