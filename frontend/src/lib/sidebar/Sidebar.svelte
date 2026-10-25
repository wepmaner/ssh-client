<script lang="ts">
  import { anyRunning } from '../forwards/forwards.svelte'
  import { hosts, keys, ui, type View } from '../stores.svelte'

  // dot marks a section with something running: a forward in the running state.
  const items: { view: View; label: string; count?: () => number; dot?: () => boolean; icon: string }[] = [
    { view: 'hosts', label: 'Хосты', count: () => hosts.length, icon: 'M2.5 3.5h11v4h-11zM2.5 8.5h11v4h-11zM4.5 5.5h.01M4.5 10.5h.01' },
    { view: 'keys', label: 'Ключи', count: () => keys.length, icon: 'M6 10a3 3 0 1 1 1.2-2.4L13.5 2.5M11 5l1.5 1.5M9.5 6.5 11 8' },
    { view: 'forwards', label: 'Пробросы', dot: anyRunning, icon: 'M2.5 5.5h9M9 3l2.5 2.5L9 8M13.5 10.5h-9M7 8l-2.5 2.5L7 13' },
  ]
  const settingsIcon =
    'M8 5.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5zM8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4M11.2 11.2l1.4 1.4M3.4 12.6l1.4-1.4M11.2 4.8l1.4-1.4'
</script>

{#snippet item(view: View, label: string, icon: string, count?: number, dot = false)}
  <button type="button" class="item" class:active={ui.view === view} aria-current={ui.view === view ? 'page' : undefined} onclick={() => (ui.view = view)}>
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d={icon} />
    </svg>
    <span class="label">{label}</span>
    {#if count !== undefined}<span class="count">{count}</span>{/if}
    {#if dot}<span class="dot" role="img" aria-label="Есть активные"></span>{/if}
  </button>
{/snippet}

<nav class="sidebar" aria-label="Разделы">
  <div class="top">
    {#each items as it (it.view)}
      {@render item(it.view, it.label, it.icon, it.count?.(), it.dot?.())}
    {/each}
  </div>
  {@render item('settings', 'Настройки', settingsIcon)}
</nav>

<style>
  .sidebar {
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    width: 212px;
    flex: none;
    padding: 10px;
    background: var(--sidebar);
    border-right: 1px solid var(--border);
  }
  .top {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .item {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 34px;
    padding: 0 10px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    font: inherit;
    text-align: left;
    cursor: default;
    transition: background-color 80ms;
  }
  .item:hover {
    background: #1e2228;
  }
  .item.active {
    background: #262b33;
    color: var(--text);
  }
  .item:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .label {
    flex: 1;
  }
  .count {
    font-size: 12px;
    color: var(--muted);
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--ok);
  }
</style>
