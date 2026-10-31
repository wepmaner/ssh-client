<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import {
    ClipboardSetText,
    EventsOn,
    HostSave,
    MCPAddCommand,
    MCPPrompt,
    MCPRotateToken,
    MCPSetEnabled,
    MCPSetPort,
    MCPStatus,
    errorText,
    type app,
  } from '../api'
  import { fieldError, userAtHost } from '../hosts/format'
  import { hosts, refreshHosts, toast } from '../stores.svelte'
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
  let prompt = $state('')

  function apply(s: Status): void {
    status = { ...s }
    port = String(s.port)
    void loadPrompt()
  }

  /** The prompt holds the port and the token, so it follows both. */
  async function loadPrompt(): Promise<void> {
    if (!status.enabled) {
      prompt = ''
      return
    }
    try {
      prompt = await MCPPrompt()
    } catch {
      prompt = ''
    }
  }

  onMount(() => {
    MCPStatus()
      .then(apply)
      .catch(() => {})
    void refreshHosts().catch(() => {})
  })
  // The server also changes by itself (a port lost, a restore from a copy).
  onDestroy(EventsOn('mcp:status', (s: Status) => apply(s)))

  async function setEnabled(on: boolean, box: HTMLInputElement): Promise<void> {
    if (busy) {
      box.checked = status.enabled
      return
    }
    busy = true
    try {
      apply(await MCPSetEnabled(on))
    } catch (e) {
      toast(errorText(e))
      box.checked = status.enabled // the failed change must not stay on screen
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
      void loadPrompt()
    } catch (e) {
      toast(errorText(e))
    }
    // A busy port after the restart shows in the status.
    void MCPStatus().then(apply).catch(() => {})
  }

  async function setAccess(h: app.HostDTO, box: HTMLSelectElement): Promise<void> {
    try {
      await HostSave({ ...h, mcp_access: box.value })
      await refreshHosts()
    } catch (e) {
      toast(errorText(e))
      box.value = h.mcp_access // the failed change must not stay on screen
    }
  }

  async function copy(get: () => Promise<string>, done: string): Promise<void> {
    try {
      await ClipboardSetText(await get())
      toast(done)
    } catch (e) {
      toast(errorText(e))
    }
  }

  const anyFull = $derived(hosts.some((h) => h.mcp_access === 'full'))
  const stateText = $derived(status.state === 'running' ? 'Работает' : status.state === 'error' ? status.error || 'Ошибка' : 'Выключен')
</script>

<div class="view">
  <section class="card" aria-labelledby="set-mcp">
    <h2 id="set-mcp">Доступ по MCP</h2>
    <label class="check">
      <input type="checkbox" checked={status.enabled} disabled={busy} onchange={(e) => setEnabled(e.currentTarget.checked, e.currentTarget)} />
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
    <div class="row">
      <Button variant="outline" onclick={() => (journal = true)}>Журнал</Button>
    </div>
  </section>

  <section class="card" aria-labelledby="set-mcp-hosts">
    <h2 id="set-mcp-hosts">Серверы</h2>
    {#if hosts.length === 0}
      <p class="hint">Хостов пока нет. Добавьте их в разделе «Хосты».</p>
    {:else}
      <ul class="hosts">
        {#each hosts as h (h.id)}
          <li>
            <span class="host">
              <span class="name">{h.name}</span>
              <span class="addr">{userAtHost(h)}</span>
            </span>
            <select class="select" aria-label={`Доступ для ${h.name}`} value={h.mcp_access} onchange={(e) => setAccess(h, e.currentTarget)}>
              <option value="">Закрыт</option>
              <option value="read">Только просмотр</option>
              <option value="full">Полный</option>
            </select>
          </li>
        {/each}
      </ul>
      {#if anyFull}
        <p class="hint">Полный доступ — всё, что может пользователь этого хоста на сервере</p>
      {/if}
    {/if}
  </section>

  <section class="card" aria-labelledby="set-mcp-connect">
    <h2 id="set-mcp-connect">Подключение</h2>
    {#if prompt}
      <pre class="prompt" aria-label="Текст промпта">{prompt}</pre>
    {/if}
    <div class="row">
      <Button variant="outline" disabled={!status.enabled} onclick={() => copy(MCPPrompt, 'Промпт скопирован')}>Скопировать промпт</Button>
      <Button variant="outline" disabled={!status.enabled} onclick={() => copy(MCPAddCommand, 'Команда скопирована')}>Скопировать команду подключения</Button>
      <Button variant="outline" disabled={!status.enabled} onclick={rotate}>Сменить токен</Button>
    </div>
    <p class="hint">При смене порта или токена подключение нужно выполнить заново.</p>
  </section>
</div>

<McpJournalDialog open={journal} onclose={() => (journal = false)} />

<style>
  .view {
    display: flex;
    flex-direction: column;
    gap: 12px;
    max-width: 820px;
    height: 100%;
    padding: 16px 24px 24px;
    overflow: auto;
  }
  .prompt {
    max-height: 260px;
    margin: 0;
    padding: 10px 12px;
    overflow: auto;
    background: #0f1114;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font-family: var(--font-mono);
    font-size: 12px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .hosts {
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .hosts li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 6px 0;
  }
  .hosts li + li {
    border-top: 1px solid var(--border);
  }
  .host {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .name,
  .addr {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .addr {
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 12px;
  }
  .select {
    flex: 0 0 170px;
    height: var(--control-height);
    padding: 0 8px;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
    color: var(--text);
    font: inherit;
  }
  .select:focus {
    outline: none;
    border-color: var(--accent);
  }
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
