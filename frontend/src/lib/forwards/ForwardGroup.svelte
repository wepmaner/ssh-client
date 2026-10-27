<script lang="ts">
  import { onDestroy, tick, untrack } from 'svelte'
  import type { app } from '../api'
  import { hosts } from '../stores.svelte'
  import { hostAsksTrust } from '../trust.svelte'
  import Button from '../ui/Button.svelte'
  import ForwardCard from './ForwardCard.svelte'
  import { outputSize, startAll, stopAll } from './forwards.svelte'
  import MiniTerminal from './MiniTerminal.svelte'

  interface Props {
    host: app.ForwardHostDTO
    /** The forwards of this host, in row order. */
    items: app.ForwardDTO[]
    onadd: () => void
    onedit: (f: app.ForwardDTO) => void
    onduplicate: (f: app.ForwardDTO) => void
    ondelete: (f: app.ForwardDTO) => void
  }
  let { host, items, onadd, onedit, onduplicate, ondelete }: Props = $props()

  const stateText: Record<string, string> = {
    none: 'не подключён',
    connecting: 'подключение',
    connected: 'подключён',
    error: 'ошибка',
  }
  const pillText = $derived(
    host.status === 'connecting'
      ? host.asker === 'forwards'
        ? 'ждёт фразу'
        : 'вход в консоли'
      : host.status === 'connected'
        ? 'подключён'
        : host.status === 'error'
          ? 'ошибка'
          : 'не подключён',
  )

  const isActive = (f: app.ForwardDTO) => f.status === 'running' || f.status === 'starting'
  const hasIdle = $derived(items.some((f) => !isActive(f)))
  const hasActive = $derived(items.some(isActive))
  const target = $derived.by(() => {
    const h = hosts.find((x) => x.id === host.host_id)
    return h?.address ? `${h.user ? h.user + '@' : ''}${h.address}` : ''
  })
  // The trust dialog asks about an unknown server key; the mini-terminal is for the prompts after it.
  const prompting = $derived(
    host.status === 'connecting' && host.asker === 'forwards' && (outputSize[host.host_id] ?? 0) > 0 && !hostAsksTrust(host.host_id),
  )

  // The login block does not vanish: it collapses, and after a successful login a line
  // «Вход выполнен» takes its place for a moment. Plain timers rather than transitionend,
  // so the state is right with reduced motion too.
  const COLLAPSE_MS = 220
  const DONE_MS = 2400
  let termMounted = $state(false)
  let termOpen = $state(false)
  let doneMounted = $state(false)
  let doneOpen = $state(false)
  /** Counts logins: a new one gets a fresh mini-terminal, never the lines of the previous one. */
  let promptKey = $state(0)
  let hadPrompt = false
  let wasPrompting = false
  let prevStatus = untrack(() => host.status)
  let termTimer: ReturnType<typeof setTimeout> | undefined
  const doneTimers: ReturnType<typeof setTimeout>[] = []
  let frames: number[] = []

  function clearDone(): void {
    doneTimers.splice(0).forEach(clearTimeout)
    frames.forEach(cancelAnimationFrame)
    frames = []
  }
  /** Runs fn after the closed state was painted, so that the CSS transition has something to start from. */
  function afterPaint(fn: () => void): void {
    void tick().then(() => {
      const a = requestAnimationFrame(() => {
        const b = requestAnimationFrame(fn)
        frames.push(b)
      })
      frames.push(a)
    })
  }
  onDestroy(() => {
    clearTimeout(termTimer)
    clearDone()
  })

  $effect(() => {
    if (prompting) {
      clearTimeout(termTimer)
      if (!wasPrompting) promptKey++
      wasPrompting = true
      // A new login: the line of the previous one has no business here any more.
      clearDone()
      doneMounted = false
      doneOpen = false
      termMounted = true
      termOpen = true
      hadPrompt = true
    } else {
      wasPrompting = false
      if (untrack(() => termMounted)) {
        termOpen = false
        clearTimeout(termTimer)
        termTimer = setTimeout(() => (termMounted = false), COLLAPSE_MS)
      }
    }
  })

  $effect(() => {
    const status = host.status
    const prev = prevStatus
    prevStatus = status
    if (prev === 'connecting' && status === 'connected' && hadPrompt) {
      clearDone()
      doneOpen = false
      doneMounted = true
      afterPaint(() => (doneOpen = true))
      doneTimers.push(setTimeout(() => (doneOpen = false), DONE_MS))
      doneTimers.push(setTimeout(() => (doneMounted = false), DONE_MS + COLLAPSE_MS))
    }
    if (status !== 'connecting') hadPrompt = false
  })
</script>

<section class="group" aria-label={host.host_name}>
  <div class="head">
    <span class="dot {host.status}" role="img" aria-label={stateText[host.status] ?? host.status}></span>
    <div class="who">
      <h2 class="name">{host.host_name}</h2>
      {#if target}<span class="addr">{target}</span>{/if}
    </div>
    <span class="pill {host.status}">{pillText}</span>
    <span class="spacer"></span>
    {#if hasIdle}
      <!-- A changed server key is final: starting again would be refused the same way. -->
      <Button
        variant="outline"
        style="height:30px;padding:0 12px"
        disabled={!host.retryable}
        title={host.retryable ? undefined : 'Ключ сервера изменился: исправьте known_hosts и перезапустите программу'}
        onclick={() => host.retryable && startAll(host.host_id)}>Запустить все</Button
      >
    {/if}
    {#if hasActive}
      <Button variant="outline" style="height:30px;padding:0 12px" onclick={() => stopAll(host.host_id)}>Остановить все</Button>
    {/if}
    <button type="button" class="act" title="Новый проброс" aria-label="Новый проброс для {host.host_name}" onclick={onadd}>
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
        <path d="M8 3v10M3 8h10" />
      </svg>
    </button>
  </div>
  {#if host.error}
    {#if host.status === 'error'}
      <div class="fail">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <circle cx="8" cy="8" r="6.5" /><path d="M8 4.5v4M8 11v.5" />
        </svg>
        <span role="alert">{host.error}</span>
      </div>
    {:else}
      <p class="note">{host.error}</p>
    {/if}
    {#if host.detail}
      <!-- The system text, as in the details of the session error card. -->
      <p class="detail">{host.detail}</p>
    {/if}
  {/if}
  {#if host.asker === 'session'}
    <p class="note">Идёт вход в консоли «{host.session}»</p>
  {/if}
  {#if termMounted}
    <div class="collapse" class:open={termOpen}>
      <div class="inner term">{#key promptKey}<MiniTerminal hostId={host.host_id} />{/key}</div>
    </div>
  {/if}
  {#if doneMounted}
    <div class="collapse" class:open={doneOpen}>
      <div class="inner">
        <div class="done" class:shown={doneOpen} role="status">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="var(--ok)" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <circle cx="8" cy="8" r="6.5" /><path d="M5 8.2l2 2 4-4.4" />
          </svg>
          <span>Вход выполнен. Фраза останется в памяти, пока подключение открыто.</span>
        </div>
      </div>
    </div>
  {/if}
  <div class="list" class:finishing={termMounted || doneMounted} role="list">
    {#each items as f, i (f.id)}
      <ForwardCard forward={f} index={i} onedit={() => onedit(f)} onduplicate={() => onduplicate(f)} ondelete={() => ondelete(f)} />
    {/each}
  </div>
</section>

<style>
  .group {
    min-width: 0;
    overflow: hidden;
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: var(--radius-dialog);
  }
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px;
    min-width: 0;
    padding: 12px 14px 12px 16px;
  }
  .who {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
  }
  .name {
    overflow: hidden;
    margin: 0;
    font-size: 14px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .addr {
    overflow: hidden;
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
    user-select: text;
  }
  .pill {
    display: inline-flex;
    align-items: center;
    flex: none;
    height: 20px;
    padding: 0 8px;
    box-sizing: border-box;
    border: 1px solid transparent;
    border-radius: 10px;
    font-size: 11.5px;
    transition:
      background-color 160ms ease,
      color 160ms ease;
  }
  .pill.none {
    border-color: #333a44;
    color: var(--muted);
  }
  .pill.connecting {
    background: #262b33;
    color: #c9d1d9;
  }
  .pill.connected {
    background: #13261a;
    color: #56d364;
  }
  .pill.error {
    background: #2b1d1d;
    color: var(--error);
  }
  .spacer {
    flex: 1;
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
  .dot.connecting {
    background: var(--muted);
    border-color: var(--muted);
    animation: pulse 1.2s ease-in-out infinite;
  }
  .dot.connected {
    background: var(--ok);
    border-color: var(--ok);
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
  .fail {
    display: flex;
    gap: 8px;
    margin: 0 16px 12px;
    color: var(--error);
    font-size: 12.5px;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .fail svg {
    flex: none;
    margin-top: 2px;
  }
  .note {
    margin: 0 16px 12px;
    color: var(--muted);
    font-size: 12px;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .detail {
    margin: -6px 16px 12px 38px;
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .collapse {
    display: grid;
    grid-template-rows: 0fr;
    opacity: 0;
    transition:
      grid-template-rows 220ms cubic-bezier(0.2, 0, 0, 1),
      opacity 160ms ease;
  }
  .collapse.open {
    grid-template-rows: 1fr;
    opacity: 1;
  }
  .inner {
    min-height: 0;
    overflow: hidden;
  }
  .term {
    padding: 0 14px;
  }
  .term > :global(.mini) {
    margin-bottom: 12px;
  }
  .done {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 16px 12px;
    color: #c9d1d9;
    opacity: 0;
    transform: translateY(-4px);
    transition:
      opacity 180ms ease 120ms,
      transform 220ms cubic-bezier(0.2, 0, 0, 1) 120ms;
  }
  .done.shown {
    opacity: 1;
    transform: none;
  }
  .list {
    border-top: 1px solid var(--border);
  }
  .act {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    cursor: default;
    transition: background-color 80ms;
  }
  .act:hover {
    background: #2a2f37;
    color: var(--text);
  }
  .act:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
</style>
