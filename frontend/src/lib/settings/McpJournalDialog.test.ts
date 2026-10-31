import { fireEvent, render, screen, waitFor } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ MCPJournalList: vi.fn(), MCPJournalClear: vi.fn() }))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})

import McpJournalDialog from './McpJournalDialog.svelte'

const rec = (id: string, extra: object = {}) => ({
  id, time: '2026-10-31T10:00:00Z', host_id: 'h1', host_name: 'srv', tool: 'run_command', text: 'docker ps', access: 'read', ...extra,
})

describe('McpJournalDialog', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('lists records with host, command and refusal', async () => {
    api.MCPJournalList.mockResolvedValue([rec('1', { exit_code: 0, duration_ms: 120 }), rec('2', { text: 'rm x', refused: 'Режим только просмотр' })])
    render(McpJournalDialog, { open: true, onclose: vi.fn() })
    await waitFor(() => expect(screen.getByText('docker ps')).toBeTruthy())
    expect(screen.getByText('rm x')).toBeTruthy()
    expect(screen.getByText('Режим только просмотр')).toBeTruthy()
    expect(screen.getAllByText('srv').length).toBe(2)
  })

  it('shows the duration of a transfer, which has no exit code', async () => {
    api.MCPJournalList.mockResolvedValue([rec('1', { tool: 'download_file', text: 'a -> b', duration_ms: 1500 })])
    render(McpJournalDialog, { open: true, onclose: vi.fn() })
    await waitFor(() => expect(screen.getByText('1.5 с')).toBeTruthy())
  })

  it('shows the exit code with the duration', async () => {
    api.MCPJournalList.mockResolvedValue([rec('1', { exit_code: 3, duration_ms: 1500 })])
    render(McpJournalDialog, { open: true, onclose: vi.fn() })
    await waitFor(() => expect(screen.getByText('Код выхода 3, 1.5 с')).toBeTruthy())
  })

  it('clears the journal', async () => {
    api.MCPJournalList.mockResolvedValueOnce([rec('1')]).mockResolvedValue([])
    api.MCPJournalClear.mockResolvedValue(undefined)
    render(McpJournalDialog, { open: true, onclose: vi.fn() })
    await waitFor(() => expect(screen.getByText('docker ps')).toBeTruthy())
    await fireEvent.click(screen.getByRole('button', { name: 'Очистить' }))
    await waitFor(() => expect(api.MCPJournalClear).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByText('docker ps')).toBeNull())
  })
})
