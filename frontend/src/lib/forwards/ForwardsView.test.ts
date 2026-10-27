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
  ForwardHostsList: vi.fn(),
  ForwardHostAttach: vi.fn(),
  ForwardHostWrite: vi.fn(),
  ForwardStartAll: vi.fn(),
  ForwardStopAll: vi.fn(),
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
import { trust } from '../trust.svelte'
import { forwards, refreshForwards } from './forwards.svelte'
import ForwardsView from './ForwardsView.svelte'

const base = { host_id: 'h1', host_name: 'prod-1', auto_start: false, error: '', conns: 0, retryable: true }
const list = [
  { ...base, id: 'f1', name: 'Postgres', kind: 'L', listen: '127.0.0.1:5432', target: 'localhost:5432', status: 'running', conns: 2 },
  { ...base, id: 'f2', name: 'SOCKS', kind: 'D', listen: '127.0.0.1:1080', target: '', status: 'stopped' },
  { ...base, id: 'f3', name: 'Вебхук', kind: 'R', listen: '127.0.0.1:9000', target: '127.0.0.1:3000', status: 'error', error: 'Порт 9000 уже занят другой программой: выберите другой порт или закройте программу, которая его держит' },
]

const hostDTO = { host_id: 'h1', host_name: 'prod-1', status: 'none', error: '', detail: '', retryable: true, asker: '', session: '' }
const card = (name: string) => screen.getByRole('article', { name })

describe('ForwardsView', () => {
  beforeEach(async () => {
    vi.resetAllMocks()
    handlers.clear()
    written.length = 0
    trust.queue.length = 0
    api.EventsOn.mockImplementation((name: string, h: Handler) => {
      handlers.set(name, h)
      return () => handlers.delete(name)
    })
    api.ForwardStart.mockResolvedValue(undefined)
    api.ForwardStop.mockResolvedValue(undefined)
    api.ForwardDelete.mockResolvedValue(undefined)
    api.ForwardAttach.mockResolvedValue(undefined)
    api.ForwardHostAttach.mockResolvedValue(undefined)
    api.ForwardHostsList.mockResolvedValue([])
    hosts.splice(0, hosts.length, { id: 'h1', name: 'prod-1', address: 'prod.example.test', port: 22, user: 'deploy' } as never)
    // An empty list drops the subscriptions of the previous test, the real one subscribes anew.
    api.ForwardsList.mockResolvedValue([])
    await refreshForwards()
    api.ForwardsList.mockResolvedValue(structuredClone(list))
    api.ForwardHostsList.mockResolvedValue([structuredClone(hostDTO)])
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
    expect(within(err).getByRole('alert').textContent).toBe('Порт 9000 уже занят другой программой: выберите другой порт или закройте программу, которая его держит')
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

  it('shows the system text under the Russian error, starting or failed', async () => {
    render(ForwardsView)
    const sys = 'dial tcp 10.0.0.1:22: connectex: No connection could be made because the target machine actively refused it.'
    handlers.get('fwd:state:f2')?.({ status: 'starting', error: 'Сервер 10.0.0.1 отклонил подключение к порту 22', detail: sys, conns: 0, retryable: true })
    await vi.waitFor(() => expect(within(card('SOCKS')).getByText(sys)).toBeTruthy())
    handlers.get('fwd:state:f2')?.({ status: 'error', error: 'Сервер 10.0.0.1 отклонил подключение к порту 22', detail: sys, conns: 0, retryable: true })
    await vi.waitFor(() => expect(within(card('SOCKS')).getByRole('alert').textContent).toBe('Сервер 10.0.0.1 отклонил подключение к порту 22'))
    expect(within(card('SOCKS')).getByText(sys).getAttribute('class')).toContain('detail')
    handlers.get('fwd:state:f2')?.({ status: 'running', error: '', detail: '', conns: 0, retryable: true })
    await vi.waitFor(() => expect(within(card('SOCKS')).queryByText(sys)).toBeNull())
  })

  it('a changed server key disables «Запустить» and keeps the error text', async () => {
    render(ForwardsView)
    const changed = 'Ключ сервера prod.example.test изменился'
    handlers.get('fwd:state:f2')?.({ status: 'error', error: changed, detail: '', conns: 0, retryable: false })
    await vi.waitFor(() => expect(within(card('SOCKS')).getByRole('alert').textContent).toBe(changed))
    const start = within(card('SOCKS')).getByRole('button', { name: 'Запустить' }) as HTMLButtonElement
    expect(start.disabled).toBe(true)
    await fireEvent.click(start)
    expect(api.ForwardStart).not.toHaveBeenCalled()
    // An ordinary failure can be started again.
    handlers.get('fwd:state:f2')?.({ status: 'error', error: 'нет сети', detail: '', conns: 0, retryable: true })
    await vi.waitFor(() => expect((within(card('SOCKS')).getByRole('button', { name: 'Запустить' }) as HTMLButtonElement).disabled).toBe(false))
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
