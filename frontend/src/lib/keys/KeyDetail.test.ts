import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  KeyCopyPublic: vi.fn(),
  KeyDelete: vi.fn(),
  KeyExport: vi.fn(),
  KeyRename: vi.fn(),
}))
vi.mock('../api', () => ({ ...api, errorText: String }))
vi.mock('../stores.svelte', () => ({ hosts: [], toast: vi.fn(), vault: { has_password: false } }))

import KeyDetail from './KeyDetail.svelte'

function key(encrypted: boolean) {
  return {
    id: 'k1',
    name: 'work',
    type: 'ed25519',
    format: encrypted ? 'ppk' : 'openssh',
    encrypted,
    public_line: 'ssh-ed25519 AAAA c',
    comment: 'c',
    fingerprint: 'SHA256:x',
    source: 'ppk',
    created_at: '2026-10-24T00:00:00Z',
    host_ids: [],
  }
}

describe('KeyDetail', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.KeyExport.mockResolvedValue(undefined)
  })

  it('marks an encrypted key and exports only the original file', async () => {
    render(KeyDetail, { key: key(true), onchanged: async () => {} })
    expect(screen.getByText('с парольной фразой')).toBeTruthy()
    expect(screen.getByText(/Фраза спрашивается при подключении/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: /OpenSSH/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /PuTTY/ })).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Приватный ключ (исходный файл)' }))
    await vi.waitFor(() => expect(api.KeyExport).toHaveBeenCalledWith('k1', 'original', ''))
  })

  it('offers OpenSSH and PPK export for an unencrypted key', async () => {
    render(KeyDetail, { key: key(false), onchanged: async () => {} })
    expect(screen.queryByText('с парольной фразой')).toBeNull()
    expect(screen.queryByText(/Фраза спрашивается при подключении/)).toBeNull()
    expect(screen.queryByRole('button', { name: 'Приватный ключ (исходный файл)' })).toBeNull()
    await fireEvent.click(screen.getByRole('button', { name: 'Приватный ключ · PuTTY .ppk' }))
    await vi.waitFor(() => expect(api.KeyExport).toHaveBeenCalledWith('k1', 'ppk', ''))
    expect(screen.getByRole('button', { name: 'Приватный ключ · OpenSSH' })).toBeTruthy()
  })
})
