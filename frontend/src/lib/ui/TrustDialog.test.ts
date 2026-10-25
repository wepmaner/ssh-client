import { fireEvent, render, screen } from '@testing-library/svelte'
import { tick } from 'svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

type Handler = (p: unknown) => void
const handlers = vi.hoisted(() => new Map<string, Handler>())
const api = vi.hoisted(() => ({
  EventsOn: vi.fn(),
  TrustAnswer: vi.fn(),
  ClipboardSetText: vi.fn(),
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})

import { trust } from '../trust.svelte'
import TrustDialog from './TrustDialog.svelte'

const fp = 'SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8'
const q1 = {
  id: 'q1',
  session_id: 's1',
  forward_id: '',
  host: 'example.test',
  port: 22,
  user: 'alice',
  key_type: 'ssh-ed25519',
  fingerprint: fp,
}
const q2 = { ...q1, id: 'q2', session_id: '', forward_id: 'f1', host: '127.0.0.1', port: 2222, user: 'deploy' }

async function emit(name: string, payload: unknown): Promise<void> {
  handlers.get(name)!(payload)
  await tick()
}

const dialog = () => screen.queryByRole('dialog', { name: 'Неизвестный сервер' })
const primary = () => screen.getByRole('button', { name: 'Доверять и подключиться' })

describe('TrustDialog', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    handlers.clear()
    trust.queue.length = 0
    api.EventsOn.mockImplementation((name: string, h: Handler) => {
      handlers.set(name, h)
      return () => handlers.delete(name)
    })
    api.TrustAnswer.mockResolvedValue(undefined)
    api.ClipboardSetText.mockResolvedValue(undefined)
    render(TrustDialog)
  })

  it('stays closed without a question', () => {
    expect(dialog()).toBeNull()
  })

  it('shows the server, the key type and the fingerprint with the primary button focused', async () => {
    await emit('trust:ask', q1)
    const d = dialog()!
    expect(d).toBeTruthy()
    expect(d.textContent).toContain('alice@example.test:22')
    expect(d.textContent).toContain('ssh-ed25519')
    expect(screen.getByText(fp)).toBeTruthy()
    expect(d.textContent).toContain(
      'Клиент видит этот сервер впервые. Если отпечаток совпадает с тем, что дал администратор, подключение безопасно.',
    )
    expect(screen.getByRole('button', { name: 'Отмена' })).toBeTruthy()
    expect(document.activeElement).toBe(primary())
  })

  it('copies the fingerprint', async () => {
    await emit('trust:ask', q1)
    await fireEvent.click(screen.getByRole('button', { name: 'Скопировать' }))
    expect(api.ClipboardSetText).toHaveBeenCalledWith(fp)
  })

  it('trusts on the primary button', async () => {
    await emit('trust:ask', q1)
    await fireEvent.click(primary())
    expect(api.TrustAnswer).toHaveBeenCalledWith('q1', true)
    expect(dialog()).toBeNull()
  })

  it('cancels on Esc and on «Отмена»', async () => {
    await emit('trust:ask', q1)
    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(api.TrustAnswer).toHaveBeenCalledTimes(1)
    expect(api.TrustAnswer).toHaveBeenCalledWith('q1', false)
    expect(dialog()).toBeNull()

    await emit('trust:ask', q2)
    await fireEvent.click(screen.getByRole('button', { name: 'Отмена' }))
    expect(api.TrustAnswer).toHaveBeenLastCalledWith('q2', false)
    expect(dialog()).toBeNull()
  })

  it('shows queued questions one at a time', async () => {
    await emit('trust:ask', q1)
    await emit('trust:ask', q2)
    expect(dialog()!.textContent).toContain('alice@example.test:22')
    await fireEvent.click(primary())
    expect(dialog()!.textContent).toContain('deploy@127.0.0.1:2222')
    expect(document.activeElement).toBe(primary())
    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(api.TrustAnswer.mock.calls).toEqual([
      ['q1', true],
      ['q2', false],
    ])
  })

  it('closes on trust:cancel and drops a withdrawn question from the queue', async () => {
    await emit('trust:ask', q1)
    await emit('trust:ask', q2)
    await emit('trust:cancel', { id: 'q2' })
    expect(dialog()!.textContent).toContain('alice@example.test:22')
    await emit('trust:cancel', { id: 'q1' })
    expect(dialog()).toBeNull()
    expect(api.TrustAnswer).not.toHaveBeenCalled()
  })
})
