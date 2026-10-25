import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  ForwardSave: vi.fn(),
  ForwardsList: vi.fn(),
  ForwardAttach: vi.fn(),
  ForwardStart: vi.fn(),
  ForwardStop: vi.fn(),
  ForwardDelete: vi.fn(),
  ForwardWrite: vi.fn(),
  EventsOn: vi.fn(() => () => {}),
}))
vi.mock('../api', async () => {
  const models = await import('../../../wailsjs/go/models')
  return { ...api, errorText: String, model: models.model }
})

import { hosts } from '../stores.svelte'
import ForwardDialog from './ForwardDialog.svelte'

const host = (id: string, name: string) => ({ id, name, address: name + '.example.test', port: 22, user: 'deploy' })
const saved = { id: 'f1', name: 'L 127.0.0.1:5432 → localhost:5432', kind: 'L', host_id: 'h1', listen: '127.0.0.1:5432', target: 'localhost:5432' }

function setup(forward: Record<string, unknown> | null = null) {
  const onclose = vi.fn()
  render(ForwardDialog, { open: true, forward: forward as never, onclose })
  return { onclose }
}

async function fill(label: string, value: string) {
  await fireEvent.input(screen.getByLabelText(label), { target: { value } })
}

function errorOf(label: string): string {
  const id = screen.getByLabelText(label).getAttribute('aria-describedby')
  return id ? (document.getElementById(id)?.textContent ?? '') : ''
}

describe('ForwardDialog', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.EventsOn.mockReturnValue(() => {})
    hosts.splice(0, hosts.length, host('h1', 'prod-1') as never, host('h2', 'db-01') as never)
    api.ForwardSave.mockResolvedValue(saved)
    api.ForwardsList.mockResolvedValue([saved])
  })

  it('defaults to L on the first host and explains the kind', () => {
    setup()
    expect(screen.getByRole('dialog', { name: 'Новый проброс' })).toBeTruthy()
    expect(screen.getByRole('radio', { name: 'L' }).getAttribute('aria-checked')).toBe('true')
    expect(screen.getByText('Порт на этом компьютере ведёт на адрес за сервером')).toBeTruthy()
    expect((screen.getByLabelText('Хост') as HTMLSelectElement).value).toBe('h1')
    expect((screen.getByLabelText('Запускать при старте приложения') as HTMLInputElement).checked).toBe(false)
  })

  it('shows the default name and hides the target for D', async () => {
    setup()
    await fill('Слушать', '127.0.0.1:5432')
    await fill('Назначение', 'localhost:5432')
    expect(screen.getByLabelText('Название').getAttribute('placeholder')).toBe('L 127.0.0.1:5432 → localhost:5432')
    await fireEvent.click(screen.getByRole('radio', { name: 'D' }))
    expect(screen.queryByLabelText('Назначение')).toBeNull()
    expect(screen.getByText('SOCKS5-прокси для браузера и программ')).toBeTruthy()
    expect(screen.getByLabelText('Название').getAttribute('placeholder')).toBe('SOCKS5 127.0.0.1:5432')
  })

  it('shows a Go validation message under the field', async () => {
    api.ForwardSave.mockRejectedValue('field:listen: Порт от 1 до 65535')
    setup()
    await fill('Слушать', '127.0.0.1:70000')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() => expect(errorOf('Слушать')).toBe('Порт от 1 до 65535'))
    expect(errorOf('Назначение')).toBe('')
    await fill('Слушать', '127.0.0.1:7000')
    expect(errorOf('Слушать')).toBe('')
  })

  it('Enter saves with the form values and closes', async () => {
    const { onclose } = setup()
    await fireEvent.change(screen.getByLabelText('Хост'), { target: { value: 'h2' } })
    await fireEvent.click(screen.getByRole('radio', { name: 'R' }))
    await fill('Слушать', '9000')
    await fill('Назначение', '127.0.0.1:3000')
    await fireEvent.click(screen.getByLabelText('Запускать при старте приложения'))
    await fireEvent.keyDown(screen.getByLabelText('Назначение'), { key: 'Enter' })
    await vi.waitFor(() => expect(onclose).toHaveBeenCalled())
    expect(api.ForwardSave).toHaveBeenCalledWith(
      expect.objectContaining({ id: '', kind: 'R', host_id: 'h2', listen: '9000', target: '127.0.0.1:3000', auto_start: true, name: '' }),
    )
  })

  it('Esc closes without saving', async () => {
    const { onclose } = setup()
    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(onclose).toHaveBeenCalled()
    expect(api.ForwardSave).not.toHaveBeenCalled()
  })

  it('edits an existing forward with its values', async () => {
    setup({ ...saved, host_id: 'h2', kind: 'D', target: '', auto_start: true, name: 'SOCKS' })
    expect(screen.getByRole('dialog', { name: 'Изменить проброс' })).toBeTruthy()
    expect(screen.getByRole('radio', { name: 'D' }).getAttribute('aria-checked')).toBe('true')
    expect((screen.getByLabelText('Хост') as HTMLSelectElement).value).toBe('h2')
    expect((screen.getByLabelText('Слушать') as HTMLInputElement).value).toBe('127.0.0.1:5432')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() =>
      expect(api.ForwardSave).toHaveBeenCalledWith(expect.objectContaining({ id: 'f1', kind: 'D', target: '', name: 'SOCKS', auto_start: true })),
    )
  })
})
