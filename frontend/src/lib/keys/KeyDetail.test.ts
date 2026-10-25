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
import source from './KeyDetail.svelte?raw'

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

  it('renames through the pencil icon next to the name', async () => {
    api.KeyRename.mockResolvedValue(undefined)
    const onchanged = vi.fn(async () => {})
    render(KeyDetail, { key: key(false), onchanged })
    const pencil = screen.getByRole('button', { name: 'Переименовать' })
    expect(pencil.textContent?.trim()).toBe('')
    expect(pencil.getAttribute('title')).toBe('Переименовать')
    expect(pencil.previousElementSibling?.tagName).toBe('H1')
    await fireEvent.click(pencil)
    const input = screen.getByRole('textbox', { name: 'Название ключа' }) as HTMLInputElement
    await fireEvent.input(input, { target: { value: 'home' } })
    await fireEvent.keyDown(input, { key: 'Enter' })
    await vi.waitFor(() => expect(api.KeyRename).toHaveBeenCalledWith('k1', 'home'))
    expect(onchanged).toHaveBeenCalled()
  })

  it('copies the public key through the icon right of its value', async () => {
    api.KeyCopyPublic.mockResolvedValue(undefined)
    render(KeyDetail, { key: key(false), onchanged: async () => {} })
    expect(screen.queryByRole('button', { name: 'Скопировать' })).toBeNull()
    const copy = screen.getByRole('button', { name: 'Скопировать публичный ключ' })
    expect(copy.textContent?.trim()).toBe('')
    expect(copy.getAttribute('title')).toBeTruthy()
    expect(copy.previousElementSibling?.tagName).toBe('PRE')
    await fireEvent.click(copy)
    await vi.waitFor(() => expect(api.KeyCopyPublic).toHaveBeenCalledWith('k1'))
  })

  it('keeps «Удалить» a text button', () => {
    render(KeyDetail, { key: key(false), onchanged: async () => {} })
    expect(screen.getByRole('button', { name: 'Удалить' }).textContent?.trim()).toBe('Удалить')
  })

  it('sizes the public key box to its content and wraps a long key inside', () => {
    const pre = /\bpre\s*\{([^}]*)\}/.exec(source)?.[1] ?? ''
    expect(pre).toMatch(/width:\s*fit-content/)
    expect(pre).toMatch(/max-width:\s*100%/)
    expect(pre).toMatch(/overflow-wrap:\s*anywhere/)
    expect(pre).not.toMatch(/word-break:\s*break-all/)
  })
})
