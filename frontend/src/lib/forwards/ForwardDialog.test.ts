import { fireEvent, render, screen } from '@testing-library/svelte'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  ForwardSave: vi.fn(),
  ForwardsList: vi.fn(),
  ForwardAttach: vi.fn(),
  ForwardStart: vi.fn(),
  ForwardStop: vi.fn(),
  ForwardDelete: vi.fn(),
  EventsOn: vi.fn(() => () => {}),
}))
vi.mock('../api', async () => {
  const model = await import('../../../bindings/sshclient/internal/model/models')
  return { ...api, errorText: String, model }
})

import { hosts } from '../stores.svelte'
import ForwardDialog from './ForwardDialog.svelte'

const host = (id: string, name: string) => ({ id, name, address: name + '.example.test', port: 22, user: 'deploy' })
const saved = { id: 'f1', name: 'L 127.0.0.1:5432 → localhost:5432', kind: 'L', host_id: 'h1', listen: '127.0.0.1:5432', target: 'localhost:5432' }

function setup(forward: Record<string, unknown> | null = null) {
  const onclose = vi.fn()
  render(ForwardDialog, { open: true, forward: forward as never, onclose })
  return { onclose }
}

async function fill(label: string, value: string) {
  await fireEvent.input(screen.getByLabelText(label), { target: { value } })
}

function errorOf(label: string): string {
  const id = screen.getByLabelText(label).getAttribute('aria-describedby')
  return id ? (document.getElementById(id)?.textContent ?? '') : ''
}

describe('ForwardDialog', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.EventsOn.mockReturnValue(() => {})
    hosts.splice(0, hosts.length, host('h1', 'prod-1') as never, host('h2', 'db-01') as never)
    api.ForwardSave.mockResolvedValue(saved)
    api.ForwardsList.mockResolvedValue([saved])
  })

  it('defaults to L on the first host and explains the kind', () => {
    setup()
    expect(screen.getByRole('dialog', { name: 'Новый проброс' })).toBeTruthy()
    expect(screen.getByRole('radio', { name: 'L' }).getAttribute('aria-checked')).toBe('true')
    expect(screen.getByText('Порт на этом компьютере ведёт на адрес за сервером')).toBeTruthy()
    expect((screen.getByLabelText('Хост') as HTMLSelectElement).value).toBe('h1')
    expect((screen.getByLabelText('Запускать при старте приложения') as HTMLInputElement).checked).toBe(false)
  })

  it('shows the default name and hides the target for D', async () => {
    setup()
    await fill('Порт здесь', '5432')
    await fill('Куда ведёт', 'localhost:5432')
    expect(screen.getByLabelText('Название').getAttribute('placeholder')).toBe('L 5432 → localhost:5432')
    await fireEvent.click(screen.getByRole('radio', { name: 'D' }))
    expect(screen.queryByLabelText('Куда ведёт')).toBeNull()
    expect(screen.getByText('SOCKS5-прокси для браузера и программ')).toBeTruthy()
    expect(screen.getByLabelText('Название').getAttribute('placeholder')).toBe('SOCKS5 5432')
  })

  it('shows a Go validation message under the field', async () => {
    api.ForwardSave.mockRejectedValue('field:listen: Порт от 1 до 65535')
    setup()
    await fill('Порт здесь', '127.0.0.1:70000')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() => expect(errorOf('Порт здесь')).toBe('Порт от 1 до 65535'))
    expect(errorOf('Куда ведёт')).toBe('')
    await fill('Порт здесь', '127.0.0.1:7000')
    expect(errorOf('Порт здесь')).toBe('')
  })

  it('Enter saves with the form values and closes', async () => {
    const { onclose } = setup()
    await fireEvent.change(screen.getByLabelText('Хост'), { target: { value: 'h2' } })
    await fireEvent.click(screen.getByRole('radio', { name: 'R' }))
    await fill('Порт на сервере', '9000')
    await fill('Куда ведёт', '127.0.0.1:3000')
    await fireEvent.click(screen.getByLabelText('Запускать при старте приложения'))
    await fireEvent.keyDown(screen.getByLabelText('Куда ведёт'), { key: 'Enter' })
    await vi.waitFor(() => expect(onclose).toHaveBeenCalled())
    expect(api.ForwardSave).toHaveBeenCalledWith(
      expect.objectContaining({ id: '', kind: 'R', host_id: 'h2', listen: '9000', target: '127.0.0.1:3000', auto_start: true, name: '' }),
    )
  })

  it('duplicate prefills fields with an empty name and saves a new forward', async () => {
    const { onclose } = (() => {
      const onclose = vi.fn()
      render(ForwardDialog, { open: true, forward: null, template: { ...saved, host_id: 'h2', auto_start: true } as never, onclose })
      return { onclose }
    })()
    expect(screen.getByRole('dialog', { name: 'Новый проброс' })).toBeTruthy()
    expect((screen.getByLabelText('Название') as HTMLInputElement).value).toBe('')
    expect((screen.getByLabelText('Хост') as HTMLSelectElement).value).toBe('h2')
    expect((screen.getByLabelText('Порт здесь') as HTMLInputElement).value).toBe('5432')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() => expect(onclose).toHaveBeenCalled())
    expect(api.ForwardSave).toHaveBeenCalledWith(
      expect.objectContaining({ id: '', name: '', kind: 'L', host_id: 'h2', listen: '5432', target: 'localhost:5432', auto_start: true }),
    )
  })

  it('edit shows a 127.0.0.1 listen as the bare port', () => {
    setup(saved)
    expect((screen.getByLabelText('Порт здесь') as HTMLInputElement).value).toBe('5432')
  })

  it('listen hint is the bare port and the note is shown', async () => {
    setup()
    expect(screen.getByLabelText('Порт здесь').getAttribute('placeholder')).toBe('5432')
    expect(screen.getByText('на 127.0.0.1')).toBeTruthy()
    api.ForwardSave.mockRejectedValue('field:listen: Порт от 1 до 65535')
    await fill('Порт здесь', '70000')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() => expect(errorOf('Порт здесь')).toBe('Порт от 1 до 65535'))
    expect(screen.queryByText('на 127.0.0.1')).toBeNull()
  })

  it('Esc closes without saving', async () => {
    const { onclose } = setup()
    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(onclose).toHaveBeenCalled()
    expect(api.ForwardSave).not.toHaveBeenCalled()
  })

  it('edits an existing forward with its values', async () => {
    setup({ ...saved, host_id: 'h2', kind: 'D', target: '', auto_start: true, name: 'SOCKS' })
    expect(screen.getByRole('dialog', { name: 'Изменить проброс' })).toBeTruthy()
    expect(screen.getByRole('radio', { name: 'D' }).getAttribute('aria-checked')).toBe('true')
    expect((screen.getByLabelText('Хост') as HTMLSelectElement).value).toBe('h2')
    expect((screen.getByLabelText('Порт здесь') as HTMLInputElement).value).toBe('5432')
    await fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await vi.waitFor(() =>
      expect(api.ForwardSave).toHaveBeenCalledWith(expect.objectContaining({ id: 'f1', kind: 'D', target: '', name: 'SOCKS', auto_start: true })),
    )
  })

  it('names the original in the subtitle of a copy and not otherwise', () => {
    const onclose = vi.fn()
    render(ForwardDialog, { open: true, forward: null, template: { ...saved, name: 'Grafana' } as never, onclose })
    expect(screen.getByText('Копия «Grafana»: поменяйте порт и сохраните')).toBeTruthy()
  })

  it('has no copy subtitle for a new forward', () => {
    setup()
    expect(screen.queryByText(/Копия/)).toBeNull()
  })

  const preview = () => [...(screen.queryByTestId('route-preview')?.children ?? [])].map((c) => c.textContent).join(' ')

  it('shows the route once the port is filled: L', async () => {
    setup()
    expect(screen.queryByTestId('route-preview')).toBeNull()
    expect(screen.getByText('адрес:порт, как его видит сервер')).toBeTruthy()
    await fill('Порт здесь', '5432')
    await fill('Куда ведёт', 'db.internal:5432')
    expect(preview()).toBe('localhost:5432 → prod-1 → db.internal:5432')
  })

  it('shows the route: R reads from the server to this computer', async () => {
    setup()
    await fireEvent.click(screen.getByRole('radio', { name: 'R' }))
    await fill('Порт на сервере', '9000')
    await fill('Куда ведёт', '127.0.0.1:3000')
    expect(preview()).toBe('127.0.0.1:3000 ← prod-1 ← порт 9000 на сервере')
    expect(screen.getByText('адрес:порт на этом компьютере')).toBeTruthy()
  })

  it('shows the route: D is a SOCKS5 proxy', async () => {
    setup()
    await fireEvent.click(screen.getByRole('radio', { name: 'D' }))
    await fill('Порт здесь', '1080')
    expect(preview()).toBe('localhost:1080 → prod-1 (SOCKS5)')
  })
})
