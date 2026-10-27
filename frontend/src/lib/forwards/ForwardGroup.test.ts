import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

type Handler = (p: unknown) => void
const handlers = vi.hoisted(() => new Map<string, Handler>())
const api = vi.hoisted(() => ({
  ForwardSave: vi.fn(),
  ForwardsList: vi.fn(),
  ForwardHostsList: vi.fn(),
  ForwardAttach: vi.fn(),
  ForwardHostAttach: vi.fn(),
  ForwardHostWrite: vi.fn(),
  ForwardStart: vi.fn(),
  ForwardStop: vi.fn(),
  ForwardStartAll: vi.fn(),
  ForwardStopAll: vi.fn(),
  ForwardDelete: vi.fn(),
  EventsOn: vi.fn(),
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

import { hosts } from '../stores.svelte'
import { trust } from '../trust.svelte'
import { refreshForwards } from './forwards.svelte'
import ForwardsView from './ForwardsView.svelte'

const fwd = (id: string, host: string, hostName: string, name: string, status = 'stopped') => ({
  id, host_id: host, host_name: hostName, name, kind: 'L', listen: '127.0.0.1:1', target: 'localhost:1',
  status, auto_start: false, error: '', conns: 0, retryable: true,
})
const host = (id: string, name: string, extra: object = {}) => ({
  host_id: id, host_name: name, status: 'none', error: '', detail: '', retryable: true, asker: '', session: '', ...extra,
})

async function load(hostList: object[], list: object[]) {
  api.ForwardsList.mockResolvedValue(structuredClone(list))
  api.ForwardHostsList.mockResolvedValue(structuredClone(hostList))
  await refreshForwards()
}

describe('ForwardGroup', () => {
  beforeEach(async () => {
    vi.resetAllMocks()
    handlers.clear()
    trust.queue.length = 0
    api.EventsOn.mockImplementation((name: string, h: Handler) => {
      handlers.set(name, h)
      return () => handlers.delete(name)
    })
    for (const f of [api.ForwardAttach, api.ForwardHostAttach, api.ForwardStartAll, api.ForwardStopAll]) f.mockResolvedValue(undefined)
    hosts.splice(
      0,
      hosts.length,
      { id: 'h1', name: 'alpha', address: 'a.example.test', port: 22, user: 'u' } as never,
      { id: 'h2', name: 'beta', address: 'b.example.test', port: 22, user: 'u' } as never,
    )
    await load([], [])
  })

  it('groups forwards by host in host-name order', async () => {
    await load(
      [host('h2', 'beta'), host('h1', 'alpha')],
      [fwd('f1', 'h2', 'beta', 'Zeta'), fwd('f2', 'h1', 'alpha', 'Web'), fwd('f3', 'h1', 'alpha', 'Api')],
    )
    render(ForwardsView)
    const groups = screen.getAllByRole('region')
    expect(groups.map((g) => g.getAttribute('aria-label'))).toEqual(['alpha', 'beta'])
    expect(within(groups[0]).getAllByRole('listitem').map((a) => a.getAttribute('aria-label'))).toEqual(['Api', 'Web'])
    expect(within(groups[1]).getAllByRole('listitem').map((a) => a.getAttribute('aria-label'))).toEqual(['Zeta'])
  })

  it('start all calls ForwardStartAll with the host id; stop all calls ForwardStopAll', async () => {
    await load([host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web', 'running'), fwd('f2', 'h1', 'alpha', 'Api')])
    render(ForwardsView)
    const g = screen.getByRole('region', { name: 'alpha' })
    await fireEvent.click(within(g).getByRole('button', { name: 'Запустить все' }))
    expect(api.ForwardStartAll).toHaveBeenCalledWith('h1')
    await fireEvent.click(within(g).getByRole('button', { name: 'Остановить все' }))
    expect(api.ForwardStopAll).toHaveBeenCalledWith('h1')
  })

  it('hides the buttons that have nothing to act on', async () => {
    await load([host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web')])
    render(ForwardsView)
    const g = screen.getByRole('region', { name: 'alpha' })
    expect(within(g).queryByRole('button', { name: 'Остановить все' })).toBeNull()
    expect(within(g).getByRole('button', { name: 'Запустить все' })).toBeTruthy()
  })

  it('start all is disabled when the host is not retryable', async () => {
    await load(
      [host('h1', 'alpha', { status: 'error', error: 'Ключ сервера изменился', retryable: false })],
      [fwd('f1', 'h1', 'alpha', 'Web')],
    )
    render(ForwardsView)
    const g = screen.getByRole('region', { name: 'alpha' })
    const start = within(g).getByRole('button', { name: 'Запустить все' }) as HTMLButtonElement
    expect(start.disabled).toBe(true)
    await fireEvent.click(start)
    expect(api.ForwardStartAll).not.toHaveBeenCalled()
    expect(within(g).getByRole('alert').textContent).toBe('Ключ сервера изменился')
  })

  it('shows the login mini-terminal in the group header while the forwards answer', async () => {
    await load([host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web', 'starting')])
    render(ForwardsView)
    handlers.get('fwdhost:state:h1')?.({ status: 'connecting', error: '', retryable: true, asker: 'forwards', session: '' })
    handlers.get('fwdhost:data:h1')?.(btoa('Password: '))
    await vi.waitFor(() => expect(within(screen.getByRole('region', { name: 'alpha' })).getByTestId('mini-terminal')).toBeTruthy())
    expect(within(screen.getByRole('listitem', { name: 'Web' })).queryByTestId('mini-terminal')).toBeNull()
    expect(api.ForwardHostAttach).toHaveBeenCalledWith('h1')
  })

  it('keeps the mini-terminal closed while the trust dialog asks for the host', async () => {
    await load([host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web', 'starting')])
    render(ForwardsView)
    trust.queue.push({ id: 'q1', session_id: '', forward_host_id: 'h1', host: 'a.example.test', port: 22, user: 'u', key_type: 'ssh-ed25519', fingerprint: 'SHA256:x' })
    handlers.get('fwdhost:state:h1')?.({ status: 'connecting', error: '', retryable: true, asker: 'forwards', session: '' })
    handlers.get('fwdhost:data:h1')?.(btoa('unknown server\r\n'))
    await vi.waitFor(() => expect(screen.getByRole('img', { name: 'подключение' })).toBeTruthy())
    expect(screen.queryByTestId('mini-terminal')).toBeNull()
    trust.queue.length = 0
    handlers.get('fwdhost:data:h1')?.(btoa('Password: '))
    await vi.waitFor(() => expect(screen.getByTestId('mini-terminal')).toBeTruthy())
  })

  it('right-click in the mini-terminal pastes and does not open a menu', async () => {
    api.ClipboardGetText.mockResolvedValue('')
    await load([host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web', 'starting')])
    render(ForwardsView)
    handlers.get('fwdhost:state:h1')?.({ status: 'connecting', error: '', retryable: true, asker: 'forwards', session: '' })
    handlers.get('fwdhost:data:h1')?.(btoa('Password: '))
    const mini = await vi.waitFor(() => screen.getByTestId('mini-terminal'))
    await fireEvent.contextMenu(mini)
    expect(screen.queryByRole('menu')).toBeNull()
    expect(api.ClipboardGetText).toHaveBeenCalled()
  })

  it('shows the console note instead when a session answers', async () => {
    await load([host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web', 'starting')])
    render(ForwardsView)
    handlers.get('fwdhost:data:h1')?.(btoa('Password: '))
    handlers.get('fwdhost:state:h1')?.({ status: 'connecting', error: '', retryable: true, asker: 'session', session: 'alpha 2' })
    await vi.waitFor(() => expect(screen.getByText('Идёт вход в консоли «alpha 2»')).toBeTruthy())
    expect(screen.queryByTestId('mini-terminal')).toBeNull()
  })

  it('plus button opens the new forward dialog with the host preselected', async () => {
    await load([host('h2', 'beta'), host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web'), fwd('f2', 'h2', 'beta', 'Api')])
    render(ForwardsView)
    await fireEvent.click(screen.getByRole('button', { name: 'Новый проброс для beta' }))
    const dialog = screen.getByRole('dialog', { name: 'Новый проброс' })
    expect((within(dialog).getByLabelText('Хост') as HTMLSelectElement).value).toBe('h2')
  })

  it('shows the host address and the state pill', async () => {
    await load([host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web')])
    render(ForwardsView)
    const g = screen.getByRole('region', { name: 'alpha' })
    expect(within(g).getByText('u@a.example.test')).toBeTruthy()
    expect(within(g).getByText('не подключён')).toBeTruthy()
    const state = (p: object) => handlers.get('fwdhost:state:h1')?.({ error: '', retryable: true, asker: '', session: '', ...p })
    state({ status: 'connecting', asker: 'forwards' })
    await vi.waitFor(() => expect(within(g).getByText('ждёт фразу')).toBeTruthy())
    state({ status: 'connecting', asker: 'session', session: 'alpha 2' })
    await vi.waitFor(() => expect(within(g).getByText('вход в консоли')).toBeTruthy())
    state({ status: 'connected' })
    await vi.waitFor(() => expect(within(g).getByText('подключён')).toBeTruthy())
    state({ status: 'error', error: 'нет сети' })
    await vi.waitFor(() => expect(within(g).getByText('ошибка')).toBeTruthy())
    expect(within(g).getByRole('alert').textContent).toBe('нет сети')
  })

  it('the switch is disabled for a stopped forward that cannot be retried', async () => {
    await load([host('h1', 'alpha')], [{ ...fwd('f1', 'h1', 'alpha', 'Web'), retryable: false }])
    render(ForwardsView)
    const sw = screen.getByRole('button', { name: 'Запустить Web' }) as HTMLButtonElement
    expect(sw.disabled).toBe(true)
    expect(sw.title).toContain('known_hosts')
    await fireEvent.click(sw)
    expect(api.ForwardStart).not.toHaveBeenCalled()
  })

  it('the success line follows the login, then goes away', async () => {
    await load([host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web', 'starting')])
    render(ForwardsView)
    vi.useFakeTimers()
    try {
      const state = (status: string) =>
        handlers.get('fwdhost:state:h1')?.({ status, error: '', retryable: true, asker: 'forwards', session: '' })
      state('connecting')
      handlers.get('fwdhost:data:h1')?.(btoa('Password: '))
      await vi.advanceTimersByTimeAsync(0)
      expect(screen.getByTestId('mini-terminal')).toBeTruthy()
      expect(screen.queryByText(/Вход выполнен/)).toBeNull()

      state('connected')
      await vi.advanceTimersByTimeAsync(0)
      expect(screen.getByText(/Вход выполнен. Фраза останется в памяти/)).toBeTruthy()
      // The prompt block collapses and is removed after the collapse.
      await vi.advanceTimersByTimeAsync(300)
      expect(screen.queryByTestId('mini-terminal')).toBeNull()
      expect(screen.getByText(/Вход выполнен/)).toBeTruthy()

      await vi.advanceTimersByTimeAsync(2400)
      await vi.advanceTimersByTimeAsync(300)
      expect(screen.queryByText(/Вход выполнен/)).toBeNull()
    } finally {
      vi.useRealTimers()
    }
  })

  it('no success line when connected without a prompt', async () => {
    await load([host('h1', 'alpha')], [fwd('f1', 'h1', 'alpha', 'Web', 'starting')])
    render(ForwardsView)
    handlers.get('fwdhost:state:h1')?.({ status: 'connecting', error: '', retryable: true, asker: 'forwards', session: '' })
    handlers.get('fwdhost:state:h1')?.({ status: 'connected', error: '', retryable: true, asker: '', session: '' })
    await vi.waitFor(() => expect(screen.getByText('подключён')).toBeTruthy())
    expect(screen.queryByText(/Вход выполнен/)).toBeNull()
  })
})
