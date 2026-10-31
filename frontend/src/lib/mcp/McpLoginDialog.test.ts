import { fireEvent, render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

type Handler = (p: unknown) => void
const handlers = vi.hoisted(() => new Map<string, Handler>())
const api = vi.hoisted(() => ({
  EventsOn: vi.fn(),
  MCPLoginWrite: vi.fn(),
  MCPLoginCancel: vi.fn(),
  MCPLoginAttach: vi.fn(),
  ClipboardGetText: vi.fn(),
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 6
    open() {}
    loadAddon() {}
    focus() {}
    dispose() {}
    attachCustomKeyEventHandler() {}
    onData() {
      return { dispose() {} }
    }
    write() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { proposeDimensions() { return undefined } } }))

import { mcpLogins } from './mcp.svelte'
import McpLoginDialog from './McpLoginDialog.svelte'

async function emit(name: string, payload: unknown): Promise<void> {
  handlers.get(name)!(payload)
  await tick()
}
const open = (host: string, login: string, name: string) => emit('mcplogin:open', { host_id: host, login_id: login, host_name: name })
const dlg = (name: string) => screen.queryByRole('dialog', { name })

describe('McpLoginDialog', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    handlers.clear()
    mcpLogins.queue.length = 0
    api.EventsOn.mockImplementation((name: string, h: Handler) => {
      handlers.set(name, h)
      return () => handlers.delete(name)
    })
    api.MCPLoginAttach.mockResolvedValue(undefined)
    api.MCPLoginCancel.mockResolvedValue(undefined)
    render(McpLoginDialog)
  })

  it('shows nothing until a login opens', () => {
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('opens for mcplogin:open, listens to its data and attaches', async () => {
    await open('h1', 'l1', 'srv')
    expect(dlg('Вход по MCP: srv')).toBeTruthy()
    expect(handlers.has('mcplogin:data:h1')).toBe(true)
    expect(api.MCPLoginAttach).toHaveBeenCalledWith('h1', 'l1')
  })

  it('cancels the login on Esc', async () => {
    await open('h1', 'l1', 'srv')
    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(api.MCPLoginCancel).toHaveBeenCalledWith('h1', 'l1')
    await tick()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(handlers.has('mcplogin:data:h1')).toBe(false)
  })

  it('queues two hosts and shows the second after the first closes', async () => {
    await open('h1', 'l1', 'alpha')
    await open('h2', 'l2', 'beta')
    expect(dlg('Вход по MCP: alpha')).toBeTruthy()
    expect(dlg('Вход по MCP: beta')).toBeNull()
    expect(api.MCPLoginAttach).toHaveBeenCalledTimes(1)
    await emit('mcplogin:close', { host_id: 'h1', login_id: 'l1' })
    expect(dlg('Вход по MCP: beta')).toBeTruthy()
    expect(api.MCPLoginAttach).toHaveBeenLastCalledWith('h2', 'l2')
    expect(api.MCPLoginCancel).not.toHaveBeenCalled()
  })

  it('ignores a close of another login id', async () => {
    await open('h1', 'l2', 'srv')
    await emit('mcplogin:close', { host_id: 'h1', login_id: 'old' })
    expect(dlg('Вход по MCP: srv')).toBeTruthy()
  })
})
