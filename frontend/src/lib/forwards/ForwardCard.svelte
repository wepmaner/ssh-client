<script lang="ts">
  import type { app } from '../api'
  import { hosts } from '../stores.svelte'
  import { forwardAsksTrust } from '../trust.svelte'
  import Button from '../ui/Button.svelte'
  import Menu from '../ui/Menu.svelte'
  import { outputSize, startForward, stopForward } from './forwards.svelte'
  import MiniTerminal from './MiniTerminal.svelte'

  interface Props {
    forward: app.ForwardDTO
    onedit: () => void
    ondelete: () => void
  }
  let { forward, onedit, ondelete }: Props = $props()

  const statusText: Record<string, string> = {
    running: 'работает',
    starting: 'подключение',
    error: 'ошибка',
    stopped: 'остановлен',
  }

  let menuOpen = $state(false)
  let menuAt = $state<{ x: number; y: number } | null>(null)
  let moreEl: HTMLButtonElement | undefined = $state()

  const active = $derived(forward.status === 'running' || forward.status === 'starting')
  // The trust dialog asks about an unknown server key; the mini-terminal is for the prompts after it.
  const prompting = $derived(
    forward.status === 'starting' && (outputSize[forward.id] ?? 0) > 0 && !forwardAsksTrust(forward.id),
  )
  const connected = $derived(hosts.find((h) => h.id === forward.host_id)?.connected ?? false)
</script>

<article
  class="card"
  class:idle={!active}
  aria-label={forward.name}
  oncontextmenu={(e) => {
    e.preventDefault()
    menuAt = { x: e.clientX, y: e.clientY }
    menuOpen = true
  }}
>
  <div class="head">
    <span class="kind kind-{forward.kind}" aria-label="Тип {forward.kind}">{forward.kind}</span>
    <span class="name">{forward.name}</span>
    <span class="spacer"></span>
    <span class="status">
      <span class="dot {forward.status}" aria-hidden="true"></span>{statusText[forward.status] ?? forward.status}
    </span>
  </div>
  <div class="route">{forward.listen} <span class="arrow">→</span> {forward.kind === 'D' ? 'SOCKS5' : forward.target}</div>
  {#if forward.status === 'error' && forward.error}
    <p class="error" role="alert">{forward.error}</p>
  {:else if forward.status === 'starting' && forward.error}
    <p class="note">{forward.error}</p>
  {/if}
  {#if (forward.status === 'error' || forward.status === 'starting') && forward.error && forward.detail}
    <!-- The system text, as in the details of the session error card. -->
    <p class="detail">{forward.detail}</p>
  {/if}
  {#if prompting}
    <MiniTerminal forwardId={forward.id} />
  {/if}
  <div class="foot">
    <span class="via">
      через <span class="host"><span class="dot" class:running={connected} aria-hidden="true"></span>{forward.host_name}</span>
    </span>
    {#if forward.status === 'running'}<span class="conns">Соединений: {forward.conns}</span>{/if}
    <span class="spacer"></span>
    {#if active}
      <Button variant="outline" onclick={() => stopForward(forward.id)}>Остановить</Button>
    {:else}
      <Button variant="primary" onclick={() => startForward(forward.id)}>Запустить</Button>
    {/if}
    <span class="more">
      <button
        type="button"
        class="act"
        title="Ещё"
        aria-label="Ещё"
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        bind:this={moreEl}
        onclick={() => {
          menuAt = null
          menuOpen = !menuOpen
        }}
      >
        <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
          <circle cx="3.5" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="12.5" cy="8" r="1.4" />
        </svg>
      </button>
      {#if menuOpen}
        <Menu
          anchor={moreEl}
          at={menuAt ?? undefined}
          onclose={() => (menuOpen = false)}
          items={[
            { label: 'Изменить', onselect: onedit },
            { label: 'Удалить', onselect: ondelete, danger: true },
          ]}
        />
      {/if}
    </span>
  </div>
</article>

<style>
  .card {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
    padding: 14px;
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
  }
  .card.idle {
    border-style: dashed;
  }
  .head,
  .foot {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }
  .foot {
    gap: 8px;
    color: var(--muted);
    font-size: 12px;
  }
  .spacer {
    flex: 1;
  }
  .kind {
    display: grid;
    place-items: center;
    flex: none;
    width: 24px;
    height: 24px;
    border-radius: var(--radius-control);
    background: #262b33;
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 500;
  }
  .kind-L {
    color: var(--accent);
  }
  .kind-R {
    background: #3a3320;
    color: #e3b341;
  }
  .kind-D {
    color: #c9a3ff;
  }
  .name {
    overflow: hidden;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .status,
  .host {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    flex: none;
  }
  .status {
    color: var(--muted);
    font-size: 12px;
  }
  .host {
    color: var(--text);
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    border: 1px solid var(--muted);
  }
  .dot.running {
    background: var(--ok);
    border-color: var(--ok);
  }
  .dot.starting {
    background: #6f7a87;
    border-color: #6f7a87;
  }
  .dot.error {
    background: var(--error);
    border-color: var(--error);
  }
  .route {
    overflow: hidden;
    font-family: var(--font-mono);
    font-size: 12.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
    user-select: text;
  }
  .arrow {
    color: var(--muted);
  }
  .error,
  .note {
    margin: 0;
    font-size: 12px;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .error {
    color: var(--error);
  }
  .detail {
    margin: -6px 0 0;
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .note {
    color: var(--muted);
  }
  .conns {
    flex: none;
  }
  .more {
    position: relative;
  }
  .act {
    display: grid;
    place-items: center;
    width: 32px;
    height: var(--control-height);
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    cursor: default;
    transition: background-color 80ms;
  }
  .act:hover,
  .act[aria-expanded='true'] {
    background: #262b33;
    color: var(--text);
  }
  .act:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
</style>
