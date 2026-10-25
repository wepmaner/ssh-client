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
  let mainEl: HTMLButtonElement | undefined = $state()
  const mono = $derived(monogram(host))

  /** Right-click opens the menu at the cursor; Shift+F10 or the menu key, at the tile. */
  function oncontextmenu(e: MouseEvent) {
    e.preventDefault()
    // A keyboard-generated contextmenu has no pointer position.
    menuAt = e.clientX === 0 && e.clientY === 0 ? null : { x: e.clientX, y: e.clientY }
    menuOpen = true
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) {
      e.preventDefault()
      menuAt = null
      menuOpen = true
    }
  }
</script>

<div class="host {layout}" class:menu-open={menuOpen} role="listitem" {oncontextmenu}>
  <button type="button" class="main" aria-haspopup="menu" bind:this={mainEl} onclick={onconnect} {onkeydown}>
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
  {#if menuOpen}
    <Menu
      anchor={mainEl}
      at={menuAt ?? undefined}
      onclose={() => (menuOpen = false)}
      items={[
        { label: 'Изменить', onselect: onedit },
        { label: 'Дублировать', onselect: onduplicate },
        { label: 'Удалить', onselect: ondelete, danger: true },
      ]}
    />
  {/if}
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
  }
  .row {
    height: 44px;
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
    padding: 0 12px 0 14px;
    border: 0;
    border-radius: inherit;
    background: transparent;
    color: var(--text);
    font: inherit;
    text-align: left;
    cursor: default;
  }
  .main:focus-visible {
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
  @media (prefers-reduced-motion: reduce) {
    .host {
      transition: none;
    }
  }
</style>
