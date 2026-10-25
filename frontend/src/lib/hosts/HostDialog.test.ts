import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  HostSave: vi.fn(),
  GroupSave: vi.fn(),
  HostsList: vi.fn(),
  GroupsList: vi.fn(),
  KeysList: vi.fn(),
  GetSettings: vi.fn(),
  VaultState: vi.fn(),
  KeyImportFile: vi.fn(),
  KeyImportFileWithPassphrase: vi.fn(),
  KeyImportText: vi.fn(),
  KeyImportTextWithPassphrase: vi.fn(),
}))
const openSession = vi.hoisted(() => vi.fn())
vi.mock('../api', async () => {
  const models = await import('../../../wailsjs/go/models')
  return { ...api, errorText: String, model: models.model }
})
vi.mock('../sessions.svelte', () => ({ openSession }))

import { groups, keys } from '../stores.svelte'
import HostDialog from './HostDialog.svelte'

const workKey = { id: 'k1', name: 'work', type: 'ed25519', encrypted: false }
const saved = { id: 'h1', name: 'srv.example.test', address: 'srv.example.test', port: 22, user: 'deploy', key_id: 'k1' }

function setup(host: Record<string, unknown> | null = null) {
  const onclose = vi.fn()
  render(HostDialog, { open: true, host: host as never, onclose })
  return { onclose }
}

async function fill(label: string, value: string) {
  await fireEvent.input(screen.getByLabelText(label), { target: { value } })
}

/** The text of the message the field points to with aria-describedby. */
function errorOf(label: string): string {
  const id = screen.getByLabelText(label).getAttribute('aria-describedby')
  return id ? (document.getElementById(id)?.textContent ?? '') : ''
}

describe('HostDialog', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    keys.splice(0, keys.length, workKey as never)
    groups.splice(0, groups.length)
    api.HostsList.mockResolvedValue([saved])
    api.GroupsList.mockResolvedValue([])
    api.KeysList.mockResolvedValue([workKey])
    api.HostSave.mockResolvedValue(saved)
  })

  it('focuses the address field on open', () => {
    setup()
    expect(document.activeElement).toBe(screen.getByLabelText('Адрес'))
  })

  it('shows a validation message under the named field', async () => {
    api.HostSave.mockRejectedValue('field:port: Порт от 1 до 65535')
    setup()
    await fill('Адрес', 'srv.example.test')
    await fill('Порт', '70000')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() => expect(errorOf('Порт')).toBe('Порт от 1 до 65535'))
    expect(errorOf('Адрес')).toBe('')
    expect(api.HostSave).toHaveBeenCalledWith(expect.objectContaining({ port: 70000 }))
    await fill('Порт', '22')
    expect(errorOf('Порт')).toBe('')
  })

  it('shows the key error under the key card', async () => {
    api.HostSave.mockRejectedValue('field:key: Выберите или импортируйте ключ')
    setup()
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() => expect(errorOf('Ключ')).toBe('Выберите или импортируйте ключ'))
  })

  it('shows the address as the default name and leaves the name to Go', async () => {
    setup()
    await fill('Адрес', '10.0.0.5')
    expect(screen.getByLabelText('Название').getAttribute('placeholder')).toBe('10.0.0.5')
    await fill('Пользователь', 'root')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() =>
      expect(api.HostSave).toHaveBeenCalledWith(
        expect.objectContaining({ address: '10.0.0.5', name: '', user: 'root', port: 22, key_id: 'k1' }),
      ),
    )
  })

  it('Enter in a field saves and connects', async () => {
    const { onclose } = setup()
    await fill('Адрес', 'srv.example.test')
    await fill('Пользователь', 'deploy')
    await fireEvent.keyDown(screen.getByLabelText('Пользователь'), { key: 'Enter' })
    await vi.waitFor(() => expect(openSession).toHaveBeenCalledWith('h1'))
    expect(onclose).toHaveBeenCalled()
  })

  it('puts an imported key into the form', async () => {
    const imported = { id: 'k2', name: 'laptop', type: 'ed25519', encrypted: true }
    api.KeyImportFile.mockResolvedValue(imported)
    api.KeysList.mockResolvedValue([workKey, imported])
    setup()
    await fireEvent.click(screen.getByRole('radio', { name: 'Импортировать' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Выбрать файл…' }))
    await vi.waitFor(() => expect((screen.getByLabelText('Ключ') as HTMLSelectElement).value).toBe('k2'))
    expect(screen.getByRole('radio', { name: 'Из хранилища' }).getAttribute('aria-checked')).toBe('true')
    await fill('Адрес', 'srv.example.test')
    await fill('Пользователь', 'deploy')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() => expect(api.HostSave).toHaveBeenCalledWith(expect.objectContaining({ key_id: 'k2' })))
  })

  it('starts on import when the vault has no keys', () => {
    keys.splice(0, keys.length)
    setup()
    expect(screen.getByRole('radio', { name: 'Импортировать' }).getAttribute('aria-checked')).toBe('true')
    expect(screen.getByRole('button', { name: 'Выбрать файл…' })).toBeTruthy()
  })

  it('edits an existing host with its values', async () => {
    setup({ ...saved, name: 'prod', port: 2222, group_id: '', group_name: '', key_name: 'work', os: '', connected: false })
    expect((screen.getByLabelText('Адрес') as HTMLInputElement).value).toBe('srv.example.test')
    expect((screen.getByLabelText('Порт') as HTMLInputElement).value).toBe('2222')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() =>
      expect(api.HostSave).toHaveBeenCalledWith(expect.objectContaining({ id: 'h1', name: 'prod', port: 2222 })),
    )
  })
})
