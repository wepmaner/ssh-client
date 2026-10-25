import { fireEvent, render, screen, within } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

type Handler = (p: unknown) => void
const handlers = vi.hoisted(() => new Map<string, Handler>())
const api = vi.hoisted(() => ({
  ForwardSave: vi.fn(),
  ForwardsList: vi.fn(),
  ForwardAttach: vi.fn(),
  ForwardStart: vi.fn(),
  ForwardStop: vi.fn(),
  ForwardDelete: vi.fn(),
  ForwardWrite: vi.fn(),
  EventsOn: vi.fn(),
  ClipboardGetText: vi.fn(),
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})
const written = vi.hoisted(() => [] as string[])
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
    write(b: Uint8Array) {
      written.push(new TextDecoder().decode(b))
    }
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { proposeDimensions() { return undefined } } }))

import Sidebar from '../sidebar/Sidebar.svelte'
import { hosts } from '../stores.svelte'
import { forwards, refreshForwards } from './forwards.svelte'
import ForwardsView from './ForwardsView.svelte'

const base = { host_id: 'h1', host_name: 'prod-1', auto_start: false, error: '', conns: 0, retryable: true }
const list = [
  { ...base, id: 'f1', name: 'Postgres', kind: 'L', listen: '127.0.0.1:5432', target: 'localhost:5432', status: 'running', conns: 2 },
  { ...base, id: 'f2', name: 'SOCKS', kind: 'D', listen: '127.0.0.1:1080', target: '', status: 'stopped' },
  { ...base, id: 'f3', name: 'Вебхук', kind: 'R', listen: '127.0.0.1:9000', target: '127.0.0.1:3000', status: 'error', error: 'не удалось занять локальный порт 127.0.0.1:9000' },
]

const card = (name: string) => screen.getByRole('article', { name })

describe('ForwardsView', () => {
  beforeEach(async () => {
    vi.resetAllMocks()
    handlers.clear()
    written.length = 0
    api.EventsOn.mockImplementation((name: string, h: Handler) => {
      handlers.set(name, h)
      return () => handlers.delete(name)
    })
    api.ForwardStart.mockResolvedValue(undefined)
    api.ForwardStop.mockResolvedValue(undefined)
    api.ForwardDelete.mockResolvedValue(undefined)
    api.ForwardAttach.mockResolvedValue(undefined)
    hosts.splice(0, hosts.length, { id: 'h1', name: 'prod-1', address: 'prod.example.test', port: 22, user: 'deploy' } as never)
    // An empty list drops the subscriptions of the previous test, the real one subscribes anew.
    api.ForwardsList.mockResolvedValue([])
    await refreshForwards()
    api.ForwardsList.mockResolvedValue(structuredClone(list))
    await refreshForwards()
  })

  it('shows the empty state with a button for the first forward', async () => {
    api.ForwardsList.mockResolvedValue([])
    await refreshForwards()
    render(ForwardsView)
    expect(screen.getByRole('heading', { name: 'Пробросов пока нет' })).toBeTruthy()
    await fireEvent.click(screen.getByRole('button', { name: 'Новый проброс' }))
    expect(screen.getByRole('dialog', { name: 'Новый проброс' })).toBeTruthy()
  })

  it('renders each card with kind, route, host, status and error', () => {
    render(ForwardsView)
    const pg = card('Postgres')
    expect(within(pg).getByText('L')).toBeTruthy()
    expect(pg.textContent).toContain('127.0.0.1:5432 → localhost:5432')
    expect(pg.textContent).toContain('prod-1')
    expect(within(pg).getByText('работает')).toBeTruthy()
    expect(pg.textContent).toContain('Соединений: 2')
    expect(card('SOCKS').textContent).toContain('127.0.0.1:1080 → SOCKS5')
    expect(within(card('SOCKS')).getByText('остановлен')).toBeTruthy()
    const err = card('Вебхук')
    expect(within(err).getByText('ошибка')).toBeTruthy()
    expect(within(err).getByRole('alert').textContent).toBe('не удалось занять локальный порт 127.0.0.1:9000')
    expect(screen.getByText('1 активен · 2 остановлены')).toBeTruthy()
  })

  it('start and stop call the API; state events update the card', async () => {
    render(ForwardsView)
    await fireEvent.click(within(card('SOCKS')).getByRole('button', { name: 'Запустить' }))
    expect(api.ForwardStart).toHaveBeenCalledWith('f2')
    await fireEvent.click(within(card('Postgres')).getByRole('button', { name: 'Остановить' }))
    expect(api.ForwardStop).toHaveBeenCalledWith('f1')
    expect(api.ForwardAttach).toHaveBeenCalledWith('f2')

    handlers.get('fwd:state:f2')?.({ status: 'running', error: '', conns: 0, retryable: true })
    await vi.waitFor(() => expect(within(card('SOCKS')).getByText('работает')).toBeTruthy())
    expect(within(card('SOCKS')).getByRole('button', { name: 'Остановить' })).toBeTruthy()
  })

  it('shows the mini-terminal while a starting forward has prompt output', async () => {
    render(ForwardsView)
    handlers.get('fwd:state:f2')?.({ status: 'starting', error: '', conns: 0, retryable: true })
    await vi.waitFor(() => expect(within(card('SOCKS')).getByText('подключение')).toBeTruthy())
    expect(within(card('SOCKS')).queryByTestId('mini-terminal')).toBeNull()
    handlers.get('fwd:data:f2')?.(btoa('Password: '))
    await vi.waitFor(() => expect(within(card('SOCKS')).getByTestId('mini-terminal')).toBeTruthy())
    expect(written.join('')).toBe('Password: ')
    handlers.get('fwd:state:f2')?.({ status: 'running', error: '', conns: 0, retryable: true })
    await vi.waitFor(() => expect(within(card('SOCKS')).queryByTestId('mini-terminal')).toBeNull())
  })

  it('right-click in the mini-terminal pastes and does not open the card menu', async () => {
    api.ClipboardGetText.mockResolvedValue('')
    render(ForwardsView)
    handlers.get('fwd:state:f2')?.({ status: 'starting', error: '', conns: 0, retryable: true })
    handlers.get('fwd:data:f2')?.(btoa('Password: '))
    const mini = await vi.waitFor(() => within(card('SOCKS')).getByTestId('mini-terminal'))
    await fireEvent.contextMenu(mini)
    expect(screen.queryByRole('menu')).toBeNull()
    expect(api.ClipboardGetText).toHaveBeenCalled()
  })

  it('deletes through the menu after confirmation', async () => {
    render(ForwardsView)
    await fireEvent.click(within(card('Postgres')).getByRole('button', { name: 'Ещё' }))
    expect(screen.getAllByRole('menuitem').map((e) => e.textContent)).toEqual(['Изменить', 'Удалить'])
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Удалить' }))
    const dialog = screen.getByRole('dialog')
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить' }))
    await vi.waitFor(() => expect(api.ForwardDelete).toHaveBeenCalledWith('f1'))
  })

  it('opens the dialog for Изменить with the forward values', async () => {
    render(ForwardsView)
    await fireEvent.click(within(card('SOCKS')).getByRole('button', { name: 'Ещё' }))
    await fireEvent.click(screen.getByRole('menuitem', { name: 'Изменить' }))
    expect(screen.getByRole('dialog', { name: 'Изменить проброс' })).toBeTruthy()
    expect((screen.getByLabelText('Слушать') as HTMLInputElement).value).toBe('127.0.0.1:1080')
  })

  it('the sidebar dot follows running forwards', async () => {
    render(Sidebar)
    expect(screen.getByRole('img', { name: 'Есть активные' })).toBeTruthy()
    handlers.get('fwd:state:f1')?.({ status: 'stopped', error: '', conns: 0, retryable: true })
    await vi.waitFor(() => expect(screen.queryByRole('img', { name: 'Есть активные' })).toBeNull())
    expect(forwards.list.find((f) => f.id === 'f1')?.status).toBe('stopped')
  })
})
