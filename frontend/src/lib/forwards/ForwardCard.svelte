<script lang="ts">
  import type { app } from '../api'
  import Menu from '../ui/Menu.svelte'
  import { shortListen } from './format'
  import { forwards, startForward, stopForward } from './forwards.svelte'

  interface Props {
    forward: app.ForwardDTO
    /** Position in the host's list: the dots light up one after another after a login. */
    index?: number
    onedit: () => void
    onduplicate: () => void
    ondelete: () => void
  }
  let { forward, index = 0, onedit, onduplicate, ondelete }: Props = $props()

  let menuOpen = $state(false)
  let menuAt = $state<{ x: number; y: number } | null>(null)
  let moreEl: HTMLButtonElement | undefined = $state()

  const active = $derived(forward.status === 'running' || forward.status === 'starting')
  // A starting forward of a host that still asks for the login waits for it.
  const waiting = $derived(
    forward.status === 'starting' && forwards.hosts.find((h) => h.host_id === forward.host_id)?.status === 'connecting',
  )
  const statusText = $derived.by(() => {
    switch (forward.status) {
      case 'running':
        return forward.conns > 0 ? `работает · ${forward.conns} соед.` : 'работает'
      case 'starting':
        return waiting ? 'ждёт вход' : 'подключение'
      case 'error':
        return 'ошибка'
      default:
        return 'остановлен'
    }
  })
  // A changed server key is final: starting again would be refused the same way.
  const blocked = $derived(!active && !forward.retryable)

  function toggle() {
    if (active) void stopForward(forward.id)
    else if (forward.retryable) void startForward(forward.id)
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
  class="row"
  role="listitem"
  aria-label={forward.name}
  style:--i={Math.min(index, 8)}
  oncontextmenu={(e) => {
    e.preventDefault()
    menuAt = { x: e.clientX, y: e.clientY }
    menuOpen = true
  }}
>
  <span class="kind kind-{forward.kind}" aria-label="Тип {forward.kind}">{forward.kind}</span>
  <div class="main">
    <span class="route" data-testid="route"><b>{shortListen(forward.listen)}</b>{" "}{#if forward.kind === 'D'}<span class="arrow">·</span> SOCKS5{:else if forward.kind === 'R'}<span class="arrow">←</span> {forward.target}{:else}<span class="arrow">→</span> {forward.target}{/if}</span>
    {#if forward.status === 'error' && forward.error}
      <p class="error" role="alert">{forward.error}</p>
    {:else if forward.status === 'starting' && forward.error}
      <p class="note">{forward.error}</p>
    {/if}
    {#if (forward.status === 'error' || forward.status === 'starting') && forward.error && forward.detail}
      <!-- The system text, as in the details of the session error card. -->
      <p class="detail">{forward.detail}</p>
    {/if}
  </div>
  <span class="name" title={forward.name}>{forward.name}</span>
  <span class="status">
    <span class="dot {forward.status}" aria-hidden="true"></span>{statusText}
  </span>
  <button
    type="button"
    class="sw"
    class:on={active}
    role="switch"
    aria-checked={active}
    aria-label={forward.name}
    disabled={blocked}
    title={blocked ? 'Ключ сервера изменился: исправьте known_hosts и перезапустите программу' : undefined}
    onclick={toggle}
  >
    <span class="knob"></span>
  </button>
  <span class="more">
    <button
      type="button"
      class="act"
      title="Ещё"
      aria-label="Ещё: {forward.name}"
      aria-haspopup="menu"
      aria-expanded={menuOpen}
      bind:this={moreEl}
      onclick={() => {
        menuAt = null
        menuOpen = !menuOpen
      }}
    >
      <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
        <circle cx="3.5" cy="8" r="1.3" /><circle cx="8" cy="8" r="1.3" /><circle cx="12.5" cy="8" r="1.3" />
      </svg>
    </button>
    {#if menuOpen}
      <Menu
        anchor={moreEl}
        at={menuAt ?? undefined}
        onclose={() => (menuOpen = false)}
        items={[
          { label: 'Изменить', onselect: onedit },
          { label: 'Дублировать', onselect: onduplicate },
          { label: 'Удалить', onselect: ondelete, danger: true },
        ]}
      />
    {/if}
  </span>
</div>

<style>
  .row {
    display: grid;
    grid-template-columns: 28px minmax(0, 1.2fr) minmax(0, 1fr) 150px 44px 32px;
    align-items: center;
    column-gap: 14px;
    padding: 9px 14px 9px 16px;
    border-bottom: 1px solid #262b33;
    transition: background-color 80ms;
  }
  .row:hover {
    background: #262b33;
  }
  .kind {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    border-radius: var(--radius-control);
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 500;
  }
  .kind-L {
    background: #1e2a3f;
    color: #7fb0ff;
  }
  .kind-R {
    background: #3a3320;
    color: #e3b341;
  }
  .kind-D {
    background: #2c2340;
    color: #c9a3ff;
  }
  .main {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .route {
    overflow: hidden;
    font-family: var(--font-mono);
    font-size: 12.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
    user-select: text;
  }
  .route b {
    font-weight: 500;
  }
  .arrow {
    color: #6f7a87;
  }
  .name {
    overflow: hidden;
    color: var(--muted);
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .status {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
    color: var(--muted);
    font-size: 12px;
    white-space: nowrap;
  }
  .dot {
    flex: none;
    width: 8px;
    height: 8px;
    box-sizing: border-box;
    border-radius: 50%;
    border: 1.5px solid #6f7a87;
    transition:
      background-color 160ms ease,
      border-color 160ms ease;
  }
  .dot.starting {
    background: var(--muted);
    border-color: var(--muted);
    animation: pulse 1.2s ease-in-out infinite;
  }
  .dot.running {
    background: var(--ok);
    border-color: var(--ok);
    /* While the group finishes a login the dots light up one after another. */
  }
  :global(.list.finishing) .dot.running {
    transition-delay: calc(var(--i) * 70ms);
  }
  .dot.error {
    background: var(--error);
    border-color: var(--error);
  }
  @keyframes pulse {
    0%,
    100% {
      opacity: 1;
    }
    50% {
      opacity: 0.35;
    }
  }
  .error,
  .note {
    margin: 3px 0 0;
    font-size: 12px;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .error {
    color: var(--error);
  }
  .note {
    color: var(--muted);
  }
  .detail {
    margin: 2px 0 0;
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .sw {
    position: relative;
    width: 40px;
    height: 22px;
    padding: 0;
    border: 1px solid #4a525e;
    border-radius: 11px;
    background: transparent;
    cursor: default;
    transition:
      background-color 120ms,
      border-color 120ms;
  }
  .sw.on {
    background: var(--accent);
    border-color: var(--accent);
  }
  .sw:disabled {
    opacity: 0.45;
  }
  .knob {
    position: absolute;
    top: 3px;
    left: 3px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: var(--muted);
    transition:
      transform 120ms cubic-bezier(0.2, 0, 0, 1),
      background-color 120ms;
  }
  .sw.on .knob {
    background: #0b1220;
    transform: translateX(18px);
  }
  .sw:focus-visible,
  .act:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  .more {
    position: relative;
  }
  .act {
    display: grid;
    place-items: center;
    width: 32px;
    height: 30px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    cursor: default;
    transition: background-color 80ms;
  }
  .act:hover,
  .act[aria-expanded='true'] {
    background: #2a2f37;
    color: var(--text);
  }
</style>
