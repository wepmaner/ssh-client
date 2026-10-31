import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  SaveSettings: vi.fn(() => Promise.resolve()),
  GetSettings: vi.fn(),
  ImportSSHConfig: vi.fn(),
  ImportPuTTY: vi.fn(),
  HostsList: vi.fn(() => Promise.resolve([])),
  GroupsList: vi.fn(() => Promise.resolve([])),
  HostsExport: vi.fn(() => Promise.resolve()),
  OpenDataFolder: vi.fn(() => Promise.resolve()),
  Version: vi.fn(() => Promise.resolve('1.4.2')),
  VaultCounts: vi.fn(() => Promise.resolve({ keys: 0, hosts: 0 })),
  VaultSetPassword: vi.fn(),
  VaultRemovePassword: vi.fn(),
  VaultState: vi.fn(),
  VaultBackup: vi.fn(() => Promise.resolve('C:\\Users\\me\\sshclient-backup-2026-10-27.vault')),
  VaultRestorePick: vi.fn(() => Promise.resolve('')),
  VaultRestore: vi.fn(() => Promise.resolve()),
  EventsOn: vi.fn(() => () => {}),
  ClipboardSetText: vi.fn(),
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})

import { model } from '../api'
import { settings, toastState, vault } from '../stores.svelte'
import SettingsView from './SettingsView.svelte'

describe('SettingsView', () => {
  it('has no MCP block: it has its own section', () => {
    render(SettingsView)
    expect(screen.queryByText('Доступ по MCP')).toBeNull()
  })

  beforeEach(() => {
    vi.clearAllMocks()
    Object.assign(settings, JSON.parse(JSON.stringify(model.Settings.createFrom({
      terminal_font: 'Cascadia Mono', terminal_size: 13, copy_on_select: true, blink_cursor: true, webgl: true, lock_after_min: 0,
    }))))
    Object.assign(vault, { ready: true, locked: false, has_password: false, corrupt: false, error: '' })
  })

  it('saves a new font at once and applies it to the store', async () => {
    render(SettingsView)
    await fireEvent.change(screen.getByLabelText('Шрифт'), { target: { value: 'JetBrains Mono' } })
    expect(api.SaveSettings).toHaveBeenCalledTimes(1)
    expect(api.SaveSettings).toHaveBeenCalledWith(expect.objectContaining({ terminal_font: 'JetBrains Mono', terminal_size: 13 }))
    expect(settings.terminal_font).toBe('JetBrains Mono')
    expect(screen.queryByRole('button', { name: 'Сохранить' })).toBeNull()
  })

  it('saves size and checkboxes', async () => {
    render(SettingsView)
    const size = screen.getByLabelText('Размер') as HTMLSelectElement
    expect([...size.options].map((o) => o.value)).toEqual(['10', '11', '12', '13', '14', '15', '16', '17', '18', '19', '20'])
    await fireEvent.change(size, { target: { value: '16' } })
    expect(api.SaveSettings).toHaveBeenLastCalledWith(expect.objectContaining({ terminal_size: 16 }))
    await fireEvent.click(screen.getByLabelText('Ускорение отрисовки (WebGL)'))
    expect(api.SaveSettings).toHaveBeenLastCalledWith(expect.objectContaining({ webgl: false, terminal_size: 16 }))
  })

  it('shows a refusal under its field and reloads the stored settings', async () => {
    api.SaveSettings.mockRejectedValueOnce('field:size: Размер от 10 до 20')
    api.GetSettings.mockResolvedValue({ ...JSON.parse(JSON.stringify(settings)), terminal_size: 13 })
    render(SettingsView)
    const size = screen.getByLabelText('Размер')
    await fireEvent.change(size, { target: { value: '16' } })
    await waitFor(() => expect(document.getElementById(size.getAttribute('aria-describedby') ?? '')?.textContent).toBe('Размер от 10 до 20'))
    await waitFor(() => expect(settings.terminal_size).toBe(13))
    // The next accepted change clears it.
    await fireEvent.change(size, { target: { value: '14' } })
    await waitFor(() => expect(size.getAttribute('aria-describedby')).toBeNull())
  })

  it('offers the idle lock only with a vault password', async () => {
    render(SettingsView)
    const lock = screen.getByLabelText('Блокировать после бездействия') as HTMLSelectElement
    expect(lock.disabled).toBe(true)
    vault.has_password = true
    await waitFor(() => expect(lock.disabled).toBe(false))
    await fireEvent.change(lock, { target: { value: '15' } })
    expect(api.SaveSettings).toHaveBeenLastCalledWith(expect.objectContaining({ lock_after_min: 15 }))
  })

  it('shows the import result and its warnings', async () => {
    api.ImportSSHConfig.mockResolvedValue({ added: 2, skipped: 1, warnings: ['ключ для web нужно импортировать отдельно: ~/.ssh/id_web'] })
    render(SettingsView)
    await fireEvent.click(screen.getByRole('button', { name: 'Хосты из ~/.ssh/config' }))
    await waitFor(() => expect(toastState.message).toBe('Добавлено хостов: 2, пропущено: 1'))
    expect(screen.getByText('ключ для web нужно импортировать отдельно: ~/.ssh/id_web')).toBeTruthy()
    expect(api.HostsList).toHaveBeenCalled()
  })

  it('shows an import error', async () => {
    api.ImportPuTTY.mockRejectedValue('Не удалось прочитать сессии PuTTY')
    render(SettingsView)
    await fireEvent.click(screen.getByRole('button', { name: 'Сессии из PuTTY' }))
    await waitFor(() => expect(toastState.message).toBe('Не удалось прочитать сессии PuTTY'))
  })

  it('shows the version', async () => {
    render(SettingsView)
    expect(await screen.findByText('Версия 1.4.2')).toBeTruthy()
  })

  it('exports hosts and opens the data folder', async () => {
    render(SettingsView)
    await fireEvent.click(screen.getByRole('button', { name: 'Экспорт хостов в файл' }))
    expect(api.HostsExport).toHaveBeenCalled()
    await fireEvent.click(screen.getByRole('button', { name: 'Открыть папку с данными' }))
    expect(api.OpenDataFolder).toHaveBeenCalled()
  })
  it('warns about the Windows-only protection only without a password', async () => {
    const warning = /Без пароля хранилище открывается только этой учётной записью Windows/
    render(SettingsView)
    expect(screen.getByText(warning)).toBeTruthy()
    vault.has_password = true
    await waitFor(() => expect(screen.queryByText(warning)).toBeNull())
  })

  it('asks a password for the backup of a vault without one', async () => {
    render(SettingsView)
    await fireEvent.click(screen.getByRole('button', { name: 'Сделать резервную копию' }))
    expect(api.VaultBackup).not.toHaveBeenCalled()
    await fireEvent.input(screen.getByLabelText('Пароль копии'), { target: { value: 'backup pass' } })
    await fireEvent.input(screen.getByLabelText('Ещё раз'), { target: { value: 'backup pass' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить копию' }))
    expect(api.VaultBackup).toHaveBeenCalledWith('backup pass')
    await waitFor(() => expect(toastState.message).toBe('Резервная копия сохранена: sshclient-backup-2026-10-27.vault'))
    await waitFor(() => expect(screen.queryByLabelText('Пароль копии')).toBeNull())
  })

  it('backs up a password vault without asking', async () => {
    vault.has_password = true
    render(SettingsView)
    await fireEvent.click(screen.getByRole('button', { name: 'Сделать резервную копию' }))
    expect(api.VaultBackup).toHaveBeenCalledWith('')
    expect(screen.queryByLabelText('Пароль копии')).toBeNull()
    await waitFor(() => expect(toastState.message).toBe('Резервная копия сохранена: sshclient-backup-2026-10-27.vault'))
  })

  it('refuses a file that is not a backup before asking the password', async () => {
    api.VaultRestorePick.mockRejectedValueOnce('Это не резервная копия sshclient')
    render(SettingsView)
    await fireEvent.click(screen.getByRole('button', { name: 'Восстановить из копии' }))
    await waitFor(() => expect(toastState.message).toBe('Это не резервная копия sshclient'))
    expect(screen.queryByLabelText('Пароль копии')).toBeNull()
  })
})
