<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import {
    ClipboardSetText,
    EventsOn,
    MCPAddCommand,
    MCPPrompt,
    MCPRotateToken,
    MCPSetEnabled,
    MCPSetPort,
    MCPStatus,
    errorText,
  } from '../api'
  import { fieldError } from '../hosts/format'
  import { toast } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import Input from '../ui/Input.svelte'
  import McpJournalDialog from './McpJournalDialog.svelte'

  /** Mirrors app.MCPStatusDTO; state is off, running or error. It never carries the token. */
  interface Status {
    enabled: boolean
    port: number
    state: string
    error: string
    detail: string
  }

  let status = $state<Status>({ enabled: false, port: 47821, state: 'off', error: '', detail: '' })
  let port = $state('47821')
  let portError = $state('')
  let journal = $state(false)
  let busy = $state(false)

  function apply(s: Status): void {
    status = { ...s }
    port = String(s.port)
  }

  onMount(() => {
    MCPStatus()
      .then(apply)
      .catch(() => {})
  })
  // The server also changes by itself (a port lost, a restore from a copy).
  onDestroy(EventsOn('mcp:status', (s: Status) => apply(s)))

  async function setEnabled(on: boolean): Promise<void> {
    if (busy) return
    busy = true
    try {
      apply(await MCPSetEnabled(on))
    } catch (e) {
      toast(errorText(e))
      void MCPStatus().then(apply).catch(() => {})
    } finally {
      busy = false
    }
  }

  async function savePort(): Promise<void> {
    portError = ''
    const text = port.trim()
    const n = /^\d+$/.test(text) ? Number(text) : -1
    try {
      apply(await MCPSetPort(n))
    } catch (e) {
      const msg = errorText(e)
      portError = fieldError(msg)?.text ?? ''
      if (!portError) toast(msg)
      port = String(status.port)
    }
  }

  async function rotate(): Promise<void> {
    try {
      await MCPRotateToken()
      toast('Токен заменён. Скопируйте команду или промпт заново')
    } catch (e) {
      toast(errorText(e))
    }
    // A busy port after the restart shows in the status.
    void MCPStatus().then(apply).catch(() => {})
  }

  async function copy(get: () => Promise<string>, done: string): Promise<void> {
    try {
      await ClipboardSetText(await get())
      toast(done)
    } catch (e) {
      toast(errorText(e))
    }
  }

  const stateText = $derived(status.state === 'running' ? 'Работает' : status.state === 'error' ? status.error || 'Ошибка' : 'Выключен')
</script>

<section class="card" aria-labelledby="set-mcp">
  <h2 id="set-mcp">Доступ по MCP</h2>
  <label class="check">
    <input type="checkbox" checked={status.enabled} disabled={busy} onchange={(e) => setEnabled(e.currentTarget.checked)} />
    Разрешить доступ к серверам по MCP
  </label>
  <div class="state" class:error={status.state === 'error'} class:running={status.state === 'running'}>
    <span class="line" role="status">{stateText}</span>
    {#if status.state === 'error' && status.detail}<span class="detail">{status.detail}</span>{/if}
  </div>
  <div class="grid">
    <label class="field">
      <span>Порт</span>
      <Input
        style="font-family:var(--font-mono)"
        inputmode="numeric"
        autocomplete="off"
        aria-describedby={portError ? 'set-mcp-port-error' : undefined}
        bind:value={port}
        oninput={() => (portError = '')}
        onchange={savePort}
      />
      {#if portError}<span class="field-error" id="set-mcp-port-error" role="alert">{portError}</span>{/if}
    </label>
  </div>
  <p class="hint">При смене порта команду подключения нужно выполнить заново.</p>
  <div class="row">
    <Button variant="outline" disabled={!status.enabled} onclick={() => copy(MCPAddCommand, 'Команда скопирована')}>Скопировать команду подключения</Button>
    <Button variant="outline" disabled={!status.enabled} onclick={() => copy(MCPPrompt, 'Промпт скопирован')}>Скопировать промпт</Button>
  </div>
  <div class="row">
    <Button variant="outline" disabled={!status.enabled} onclick={rotate}>Сменить токен</Button>
    <Button variant="outline" onclick={() => (journal = true)}>Журнал</Button>
  </div>
</section>

<McpJournalDialog open={journal} onclose={() => (journal = false)} />

<style>
  .card {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 16px 18px;
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
  }
  h2 {
    margin: 0;
    font-size: 14px;
    font-weight: 600;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 10px;
    width: fit-content;
  }
  .check input {
    margin: 0;
    accent-color: var(--accent);
  }
  .state {
    display: flex;
    flex-direction: column;
    gap: 2px;
    color: var(--muted);
    font-size: 12px;
  }
  .state.running .line {
    color: var(--ok);
  }
  .state.error .line {
    color: var(--error);
  }
  .detail {
    font-family: var(--font-mono);
    user-select: text;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px 16px;
    max-width: 460px;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .field > span {
    font-size: 12px;
    color: var(--muted);
  }
  .field > .field-error {
    color: var(--error);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .hint {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
  }
  .field-error {
    color: var(--error);
    font-size: 12px;
  }
</style>
