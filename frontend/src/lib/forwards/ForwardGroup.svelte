<script lang="ts">
  import type { app } from '../api'
  import { hostAsksTrust } from '../trust.svelte'
  import Button from '../ui/Button.svelte'
  import ForwardCard from './ForwardCard.svelte'
  import { outputSize, startAll, stopAll } from './forwards.svelte'
  import MiniTerminal from './MiniTerminal.svelte'

  interface Props {
    host: app.ForwardHostDTO
    /** The forwards of this host, in card order. */
    items: app.ForwardDTO[]
    onadd: () => void
    onedit: (f: app.ForwardDTO) => void
    ondelete: (f: app.ForwardDTO) => void
  }
  let { host, items, onadd, onedit, ondelete }: Props = $props()

  const stateText: Record<string, string> = {
    none: 'не подключён',
    connecting: 'подключение',
    connected: 'подключён',
    error: 'ошибка',
  }

  const isActive = (f: app.ForwardDTO) => f.status === 'running' || f.status === 'starting'
  const hasIdle = $derived(items.some((f) => !isActive(f)))
  const hasActive = $derived(items.some(isActive))
  // The trust dialog asks about an unknown server key; the mini-terminal is for the prompts after it.
  const prompting = $derived(
    host.status === 'connecting' && host.asker === 'forwards' && (outputSize[host.host_id] ?? 0) > 0 && !hostAsksTrust(host.host_id),
  )
</script>

<section class="group" aria-label={host.host_name}>
  <div class="head">
    <span class="dot {host.status}" role="img" aria-label={stateText[host.status] ?? host.status}></span>
    <h2 class="name">{host.host_name}</h2>
    <span class="spacer"></span>
    {#if hasIdle}
      <!-- A changed server key is final: starting again would be refused the same way. -->
      <Button
        variant="primary"
        disabled={!host.retryable}
        title={host.retryable ? undefined : 'Ключ сервера изменился: исправьте known_hosts и перезапустите программу'}
        onclick={() => host.retryable && startAll(host.host_id)}>Запустить все</Button
      >
    {/if}
    {#if hasActive}
      <Button variant="outline" onclick={() => stopAll(host.host_id)}>Остановить все</Button>
    {/if}
    <button type="button" class="act" title="Новый проброс" aria-label="Новый проброс для {host.host_name}" onclick={onadd}>
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
        <path d="M8 3v10M3 8h10" />
      </svg>
    </button>
  </div>
  {#if host.error}
    <p class={host.status === 'error' ? 'error' : 'note'} role={host.status === 'error' ? 'alert' : undefined}>{host.error}</p>
    {#if host.detail}
      <!-- The system text, as in the details of the session error card. -->
      <p class="detail">{host.detail}</p>
    {/if}
  {/if}
  {#if host.asker === 'session'}
    <p class="note">Идёт вход в консоли «{host.session}»</p>
  {:else if prompting}
    <MiniTerminal hostId={host.host_id} />
  {/if}
  <div class="grid">
    {#each items as f (f.id)}
      <ForwardCard forward={f} onedit={() => onedit(f)} ondelete={() => ondelete(f)} />
    {/each}
  </div>
</section>

<style>
  .group {
    display: flex;
    flex-direction: column;
    gap: 10px;
    min-width: 0;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
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
  .spacer {
    flex: 1;
  }
  .dot {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    border: 1px solid var(--muted);
  }
  .dot.connecting {
    background: #6f7a87;
    border-color: #6f7a87;
  }
  .dot.connected {
    background: var(--ok);
    border-color: var(--ok);
  }
  .dot.error {
    background: var(--error);
    border-color: var(--error);
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
  .note {
    color: var(--muted);
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
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(380px, 100%), 1fr));
    gap: 10px;
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
  .act:hover {
    background: #262b33;
    color: var(--text);
  }
  .act:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
</style>
