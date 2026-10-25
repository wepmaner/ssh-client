<script lang="ts">
  import type { app } from '../api'
  import Menu from '../ui/Menu.svelte'
  import { monogram, userAtHost } from './format'

  interface Props {
    host: app.HostDTO
    /** tile: the 64 px card of the grid; row: a line of the list view. */
    layout: 'tile' | 'row'
    onconnect: () => void
    onedit: () => void
    onduplicate: () => void
    ondelete: () => void
  }
  let { host, layout, onconnect, onedit, onduplicate, ondelete }: Props = $props()

  let menuOpen = $state(false)
  let menuAt = $state<{ x: number; y: number } | null>(null)
  let moreEl: HTMLButtonElement | undefined = $state()
  const mono = $derived(monogram(host))
</script>

<div
  class="host {layout}"
  class:menu-open={menuOpen}
  role="listitem"
  oncontextmenu={(e) => {
    e.preventDefault()
    menuAt = { x: e.clientX, y: e.clientY }
    menuOpen = true
  }}
>
  <button type="button" class="main" onclick={onconnect}>
    <span class="mono" style:background={mono.color} aria-hidden="true" data-testid="monogram">{mono.text}</span>
    <span class="info">
      <span class="name">
        <span class="name-text" data-testid="host-name">{host.name}</span>
        {#if host.connected}<span class="dot" role="img" aria-label="Подключено" title="Подключено"></span>{/if}
      </span>
      <span class="sub">{userAtHost(host)}</span>
    </span>
    {#if layout === 'row'}
      <span class="col">{host.group_name}</span>
      <span class="col">{host.key_name}</span>
    {/if}
  </button>
  <span class="actions">
    <button type="button" class="act connect" title="Подключить" aria-label="Подключить" onclick={onconnect}>
      <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true"><path d="M5 3.5v9l7-4.5z" fill="currentColor" /></svg>
    </button>
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
          { label: 'Дублировать', onselect: onduplicate },
          { label: 'Удалить', onselect: ondelete, danger: true },
        ]}
      />
    {/if}
  </span>
</div>

<style>
  .host {
    position: relative;
    display: flex;
    align-items: center;
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
    transition:
      background-color 80ms,
      border-color 80ms;
  }
  .host:hover,
  .host.menu-open {
    background: #262b33;
    border-color: #3a414b;
  }
  .tile {
    height: 64px;
    padding-right: 12px;
  }
  .row {
    height: 44px;
    padding-right: 8px;
    border-radius: 0;
    border-width: 0 0 1px;
    background: transparent;
  }
  .main {
    display: flex;
    flex: 1;
    align-items: center;
    gap: 12px;
    min-width: 0;
    height: 100%;
    padding: 0 0 0 14px;
    border: 0;
    border-radius: inherit;
    background: transparent;
    color: var(--text);
    font: inherit;
    text-align: left;
    cursor: default;
  }
  .main:focus-visible,
  .act:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  .mono {
    display: grid;
    place-items: center;
    flex: 0 0 40px;
    height: 40px;
    border-radius: var(--radius-dialog);
    color: #fff;
    font-size: 13px;
    font-weight: 600;
  }
  .row .mono {
    flex-basis: 28px;
    height: 28px;
    border-radius: var(--radius-card);
    font-size: 11px;
  }
  .info {
    display: flex;
    flex-direction: column;
    gap: 3px;
    flex: 1;
    min-width: 0;
  }
  .row .info {
    flex-direction: row;
    align-items: baseline;
    gap: 12px;
  }
  .name {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
    font-weight: 600;
  }
  .row .name {
    flex: 0 0 200px;
  }
  .name-text,
  .sub,
  .col {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .dot {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--ok);
  }
  .sub {
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11.5px;
  }
  .col {
    flex: 0 0 140px;
    color: var(--muted);
    font-size: 12px;
  }
  .actions {
    position: relative;
    display: flex;
    gap: 2px;
    opacity: 0;
    transition: opacity 80ms;
  }
  .host:hover .actions,
  .host:focus-within .actions,
  .host.menu-open .actions {
    opacity: 1;
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
  .act.connect {
    background: #2e333b;
    color: var(--text);
  }
  .act:hover {
    background: #333a44;
    color: var(--text);
  }
  @media (prefers-reduced-motion: reduce) {
    .host,
    .actions,
    .act {
      transition: none;
    }
  }
</style>
