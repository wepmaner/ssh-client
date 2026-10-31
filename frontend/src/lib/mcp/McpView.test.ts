import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

type Handler = (p: unknown) => void
const handlers = vi.hoisted(() => new Map<string, Handler>())
const api = vi.hoisted(() => ({
  EventsOn: vi.fn(),
  MCPStatus: vi.fn(),
  MCPSetEnabled: vi.fn(),
  MCPSetPort: vi.fn(),
  MCPRotateToken: vi.fn(),
  MCPPrompt: vi.fn(),
  MCPAddCommand: vi.fn(),
  MCPJournalList: vi.fn(),
  MCPJournalClear: vi.fn(),
  ClipboardSetText: vi.fn(),
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})

import { toastState } from '../stores.svelte'
import McpView from './McpView.svelte'

const off = { enabled: false, port: 47821, state: 'off', error: '', detail: '' }
const on = { enabled: true, port: 47821, state: 'running', error: '', detail: '' }
const SECRET = 'tok-SECRET-123'

async function mount(status = off) {
  api.MCPStatus.mockResolvedValue(status)
  const r = render(McpView)
  await waitFor(() => expect(api.MCPStatus).toHaveBeenCalled())
  await tick()
  return r
}

describe('McpView', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    handlers.clear()
    toastState.message = ''
    api.EventsOn.mockImplementation((name: string, h: Handler) => {
      handlers.set(name, h)
      return () => handlers.delete(name)
    })
    api.MCPAddCommand.mockResolvedValue(`claude mcp add ... Bearer ${SECRET}`)
    api.MCPPrompt.mockResolvedValue(`prompt with ${SECRET}`)
    api.ClipboardSetText.mockResolvedValue(undefined)
    api.MCPJournalList.mockResolvedValue([])
  })

  it('switches the server through MCPSetEnabled and shows the state', async () => {
    await mount()
    expect(screen.getByText('Выключен')).toBeTruthy()
    api.MCPSetEnabled.mockResolvedValue(on)
    await fireEvent.click(screen.getByLabelText('Разрешить доступ к серверам по MCP'))
    expect(api.MCPSetEnabled).toHaveBeenCalledWith(true)
    await waitFor(() => expect(screen.getByText('Работает')).toBeTruthy())
  })

  it('puts the checkbox back when the switch fails', async () => {
    await mount()
    api.MCPSetEnabled.mockRejectedValue('Хранилище заблокировано')
    const box = screen.getByLabelText('Разрешить доступ к серверам по MCP') as HTMLInputElement
    await fireEvent.click(box)
    await waitFor(() => expect(api.MCPSetEnabled).toHaveBeenCalled())
    await waitFor(() => expect(box.checked).toBe(false))
  })

  it('keeps the copy buttons off while the switch is off', async () => {
    await mount()
    expect((screen.getByRole('button', { name: 'Скопировать команду подключения' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Скопировать промпт' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: 'Сменить токен' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('puts the command and the prompt on the clipboard', async () => {
    await mount(on)
    await fireEvent.click(screen.getByRole('button', { name: 'Скопировать команду подключения' }))
    await waitFor(() => expect(api.ClipboardSetText).toHaveBeenCalledWith(`claude mcp add ... Bearer ${SECRET}`))
    await fireEvent.click(screen.getByRole('button', { name: 'Скопировать промпт' }))
    await waitFor(() => expect(api.ClipboardSetText).toHaveBeenLastCalledWith(`prompt with ${SECRET}`))
  })

  it('shows a start error with its system text', async () => {
    await mount({ enabled: true, port: 47821, state: 'error', error: 'Порт 47821 занят другой программой', detail: 'bind: in use' })
    expect(screen.getByText('Порт 47821 занят другой программой')).toBeTruthy()
    expect(screen.getByText('bind: in use')).toBeTruthy()
  })

  it('follows mcp:status events', async () => {
    await mount(on)
    handlers.get('mcp:status')!({ ...on, state: 'error', error: 'Порт занят' })
    await tick()
    expect(screen.getByText('Порт занят')).toBeTruthy()
  })

  it('rotates the token and says to repeat the command', async () => {
    await mount(on)
    api.MCPRotateToken.mockResolvedValue(undefined)
    api.MCPStatus.mockResolvedValue(on)
    await fireEvent.click(screen.getByRole('button', { name: 'Сменить токен' }))
    await waitFor(() => expect(api.MCPRotateToken).toHaveBeenCalled())
    await waitFor(() => expect(toastState.message).toContain('Скопируйте команду'))
  })

  it('saves the port and shows its refusal under the field', async () => {
    await mount(on)
    api.MCPSetPort.mockRejectedValue('field:port: Порт от 1024 до 65535')
    const port = screen.getByLabelText('Порт')
    await fireEvent.input(port, { target: { value: '80' } })
    await fireEvent.change(port)
    expect(api.MCPSetPort).toHaveBeenCalledWith(80)
    await waitFor(() => expect(screen.getByRole('alert').textContent).toBe('Порт от 1024 до 65535'))
  })

  it('shows the exact prompt beside its copy button', async () => {
    await mount(on)
    await waitFor(() => expect(screen.getByLabelText('Текст промпта').textContent).toBe(`prompt with ${SECRET}`))
  })

  it('shows no prompt while the switch is off', async () => {
    await mount()
    expect(screen.queryByLabelText('Текст промпта')).toBeNull()
    expect(api.MCPPrompt).not.toHaveBeenCalled()
  })

  it('shows the new prompt after the token changes', async () => {
    await mount(on)
    await waitFor(() => expect(screen.getByLabelText('Текст промпта').textContent).toContain(SECRET))
    api.MCPRotateToken.mockResolvedValue(undefined)
    api.MCPStatus.mockResolvedValue(on)
    api.MCPPrompt.mockResolvedValue('prompt with NEW')
    await fireEvent.click(screen.getByRole('button', { name: 'Сменить токен' }))
    await waitFor(() => expect(screen.getByLabelText('Текст промпта').textContent).toBe('prompt with NEW'))
  })

  it('does not name Claude outside the prompt', async () => {
    await mount(on)
    await waitFor(() => expect(screen.getByLabelText('Текст промпта')).toBeTruthy())
    screen.getByLabelText('Текст промпта').remove()
    expect(document.body.textContent).not.toMatch(/claude/i)
  })

  it('opens the journal', async () => {
    await mount(on)
    await fireEvent.click(screen.getByRole('button', { name: 'Журнал' }))
    await waitFor(() => expect(screen.getByRole('dialog', { name: 'Журнал MCP' })).toBeTruthy())
    expect(api.MCPJournalList).toHaveBeenCalled()
  })
})
