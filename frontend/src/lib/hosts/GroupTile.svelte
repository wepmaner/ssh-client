<script lang="ts">
  import type { app } from '../api'
  import { hostsLabel } from '../keys/format'
  import Menu from '../ui/Menu.svelte'
  import { connectedLabel } from './format'

  interface Props {
    group: app.GroupDTO
    /** The host list is filtered by this group. */
    selected: boolean
    onselect: () => void
    onrename: () => void
    ondelete: () => void
  }
  let { group, selected, onselect, onrename, ondelete }: Props = $props()

  let menuOpen = $state(false)
  let moreEl: HTMLButtonElement | undefined = $state()
</script>

<div
  class="group"
  class:selected
  class:menu-open={menuOpen}
  role="listitem"
  oncontextmenu={(e) => {
    e.preventDefault()
    menuOpen = true
  }}
>
  <button type="button" class="main" aria-pressed={selected} onclick={onselect}>
    <span class="icon" aria-hidden="true">
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round">
        <path d="M2 4.5A1.5 1.5 0 0 1 3.5 3h3l1.5 1.5h4.5A1.5 1.5 0 0 1 14 6v6a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 12z" />
      </svg>
    </span>
    <span class="info">
      <span class="name">{group.name}</span>
      <span class="sub">
        {hostsLabel(group.host_count)}{group.connected_count > 0 ? ' · ' + connectedLabel(group.connected_count) : ''}
      </span>
    </span>
  </button>
  <span class="actions">
    <button
      type="button"
      class="act"
      title="Ещё"
      aria-label="Ещё о группе {group.name}"
      aria-haspopup="menu"
      aria-expanded={menuOpen}
      bind:this={moreEl}
      onclick={() => (menuOpen = !menuOpen)}
    >
      <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
        <circle cx="3.5" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="12.5" cy="8" r="1.4" />
      </svg>
    </button>
    {#if menuOpen}
      <Menu
        anchor={moreEl}
        onclose={() => (menuOpen = false)}
        items={[
          { label: 'Переименовать', onselect: onrename },
          { label: 'Удалить', onselect: ondelete, danger: true },
        ]}
      />
    {/if}
  </span>
</div>

<style>
  .group {
    position: relative;
    display: flex;
    align-items: center;
    height: 56px;
    padding-right: 10px;
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
    transition:
      background-color 80ms,
      border-color 80ms;
  }
  .group:hover,
  .group.menu-open {
    background: #262b33;
    border-color: #3a414b;
  }
  .group.selected {
    border-color: var(--accent);
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
  .icon {
    display: grid;
    place-items: center;
    flex: 0 0 32px;
    height: 32px;
    border-radius: var(--radius-card);
    background: color-mix(in srgb, var(--accent) 22%, var(--card));
    color: var(--accent);
  }
  .info {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .name,
  .sub {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .name {
    font-weight: 600;
  }
  .sub {
    color: var(--muted);
    font-size: 11px;
  }
  .actions {
    position: relative;
    opacity: 0;
    transition: opacity 80ms;
  }
  .group:hover .actions,
  .group:focus-within .actions,
  .group.menu-open .actions {
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
  .act:hover {
    background: #333a44;
    color: var(--text);
  }
  @media (prefers-reduced-motion: reduce) {
    .group,
    .actions,
    .act {
      transition: none;
    }
  }
</style>
