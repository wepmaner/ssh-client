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

import { registerTerminalFocus, sessions } from '../sessions.svelte'
import { toastState, ui } from '../stores.svelte'
import { hostPort, trust } from '../trust.svelte'
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
const cancel = () => screen.getByRole('button', { name: 'Отмена' })

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

  it('shows the server, the key type and the fingerprint with «Отмена» focused', async () => {
    await emit('trust:ask', q1)
    const d = dialog()!
    expect(d).toBeTruthy()
    expect(d.textContent).toContain('alice@example.test:22')
    expect(d.textContent).toContain('ssh-ed25519')
    expect(screen.getByText(fp)).toBeTruthy()
    expect(d.textContent).toContain(
      'Клиент видит этот сервер впервые. Если отпечаток совпадает с тем, что дал администратор, подключение безопасно.',
    )
    // A security prompt defaults to the safe answer; trusting takes a deliberate click or Tab.
    expect(document.activeElement).toBe(cancel())
    expect(primary().className).toContain('primary')
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
    expect(document.activeElement).toBe(cancel())
    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(api.TrustAnswer.mock.calls).toEqual([
      ['q1', true],
      ['q2', false],
    ])
  })

  it('ignores a repeated trust:ask with the same id', async () => {
    await emit('trust:ask', q1)
    await emit('trust:ask', { ...q1 })
    expect(trust.queue).toHaveLength(1)
    await fireEvent.click(primary())
    expect(dialog()).toBeNull()
    expect(api.TrustAnswer).toHaveBeenCalledTimes(1)
  })

  it('brackets an IPv6 host like JoinHostPort', async () => {
    expect(hostPort('example.test', 22)).toBe('example.test:22')
    expect(hostPort('2001:db8::1', 2222)).toBe('[2001:db8::1]:2222')
    await emit('trust:ask', { ...q1, host: '::1' })
    expect(dialog()!.textContent).toContain('alice@[::1]:22')
  })

  it('gives focus back to where it was after an answer and after a cancel', async () => {
    const before = document.createElement('button')
    document.body.append(before)
    try {
      for (const close of [() => fireEvent.click(primary()), () => fireEvent.keyDown(window, { key: 'Escape' })]) {
        before.focus()
        await emit('trust:ask', { ...q2, id: 'q' + Math.random() })
        expect(document.activeElement).toBe(cancel())
        await close()
        expect(dialog()).toBeNull()
        await vi.waitFor(() => expect(document.activeElement).toBe(before))
      }
    } finally {
      before.remove()
    }
  })

  it('focuses the terminal of the active session it asked for', async () => {
    const focus = vi.fn()
    const off = registerTerminalFocus('s1', focus)
    sessions.activeId = 's1'
    ui.activeTab = 'session'
    try {
      await emit('trust:ask', q1)
      await fireEvent.click(primary())
      await vi.waitFor(() => expect(focus).toHaveBeenCalled())
    } finally {
      off()
      ui.activeTab = 'home'
      sessions.activeId = ''
    }
  })

  it('shows no toast when the question was withdrawn while the answer was on its way', async () => {
    toastState.message = ''
    let reject!: (e: unknown) => void
    api.TrustAnswer.mockReturnValue(new Promise((_, rj) => (reject = rj)))
    await emit('trust:ask', q1)
    await fireEvent.click(primary())
    await emit('trust:cancel', { id: 'q1' })
    reject('Вопрос о доверии серверу уже снят')
    await new Promise((r) => setTimeout(r, 0))
    expect(toastState.message).toBe('')

    api.TrustAnswer.mockRejectedValue('сбой')
    await emit('trust:ask', q2)
    await fireEvent.click(primary())
    await vi.waitFor(() => expect(toastState.message).toBe('сбой'))
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
