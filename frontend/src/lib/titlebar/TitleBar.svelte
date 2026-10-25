<script lang="ts">
  import { WindowClose, WindowMinimise, WindowToggleMaximise } from '../api'
  import { activate, activeSession, closeSession, cycleTab, goHome, sessions } from '../sessions.svelte'
  import { ui } from '../stores.svelte'
  import { tabAction } from '../terminal/keys'

  /** title is the home tab label; tabs adds the session tabs and their shortcuts. */
  let { title = '', tabs = false }: { title?: string; tabs?: boolean } = $props()

  const active = $derived(tabs ? activeSession() : undefined)

  function onkeydown(e: KeyboardEvent): void {
    if (!tabs || document.querySelector('[role="dialog"]')) return
    const action = tabAction(e)
    if (!action) return
    e.preventDefault()
    if (action === 'next') cycleTab(1)
    else if (action === 'prev') cycleTab(-1)
    else if (active) closeSession(active.id)
  }

  function newSession(): void {
    ui.view = 'hosts'
    goHome()
  }

  function toggleSearch(): void {
    if (active) sessions.searchId = sessions.searchId === active.id ? '' : active.id
  }

  const stop = (e: Event) => e.stopPropagation()
</script>

<svelte:window {onkeydown} />

<header class="titlebar" role="presentation" ondblclick={WindowToggleMaximise}>
  <div class="left">
    <svg class="glyph" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M3 4.5 7 8l-4 3.5" />
      <path d="M8.5 12H13" />
    </svg>
    {#if title}
      <div class="tabs" role="tablist">
        <button type="button" role="tab" class="tab home" class:active={!active} aria-selected={!active} onclick={goHome} ondblclick={stop}>{title}</button>
        {#if tabs}
          {#each sessions.list as tab (tab.id)}
            <div class="tab session" class:active={active?.id === tab.id}>
              <button type="button" role="tab" class="select" aria-selected={active?.id === tab.id} onclick={() => activate(tab.id)} ondblclick={stop}>
                <span class="dot {tab.state}" aria-hidden="true"></span>
                <span class="name">{tab.hostName}</span>
              </button>
              <button type="button" class="close-tab" aria-label="Закрыть вкладку {tab.hostName}" onclick={() => closeSession(tab.id)} ondblclick={stop}>
                <svg width="8" height="8" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
                  <path d="M1 1l8 8M9 1 1 9" />
                </svg>
              </button>
            </div>
          {/each}
          <button type="button" class="plus" aria-label="Новая сессия" onclick={newSession} ondblclick={stop}>
            <svg width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" aria-hidden="true">
              <path d="M6 1.5v9M1.5 6h9" />
            </svg>
          </button>
        {/if}
      </div>
    {/if}
  </div>
  <div class="caption">
    {#if active}
      <button type="button" class="action" class:on={sessions.searchId === active.id} aria-label="Поиск в терминале" title="Поиск (Ctrl+F)" onclick={toggleSearch} ondblclick={stop}>
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" aria-hidden="true">
          <circle cx="7" cy="7" r="4.5" />
          <path d="m10.5 10.5 3 3" />
        </svg>
      </button>
    {/if}
    <button type="button" class="btn" aria-label="Свернуть" onclick={WindowMinimise} ondblclick={stop}>
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1" aria-hidden="true">
        <path d="M0 5.5h10" />
      </svg>
    </button>
    <button type="button" class="btn" aria-label="Развернуть" onclick={WindowToggleMaximise} ondblclick={stop}>
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1" aria-hidden="true">
        <rect x="0.5" y="0.5" width="9" height="9" />
      </svg>
    </button>
    <button type="button" class="btn close" aria-label="Закрыть" onclick={WindowClose} ondblclick={stop}>
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1" aria-hidden="true">
        <path d="M0 0l10 10M10 0 0 10" />
      </svg>
    </button>
  </div>
</header>

<style>
  .titlebar {
    display: flex;
    justify-content: space-between;
    align-items: stretch;
    height: var(--titlebar-height);
    flex: none;
    background: var(--titlebar);
    border-bottom: 1px solid var(--border);
    color: var(--text);
    --wails-draggable: drag;
  }

  .left {
    display: flex;
    align-items: center;
    min-width: 0;
    padding-left: 12px;
  }

  .glyph {
    flex: none;
    color: var(--accent);
  }

  .tabs {
    display: flex;
    align-items: flex-end;
    align-self: stretch;
    gap: 2px;
    min-width: 0;
    margin-left: 12px;
  }

  .tab {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 32px;
    margin-bottom: -1px;
    border: 0;
    border-bottom: 1px solid transparent;
    border-radius: 6px 6px 0 0;
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: 13px;
    cursor: default;
    --wails-draggable: no-drag;
  }
  .tab:hover:not(.active) {
    background: #1c1f24;
    color: var(--text);
  }
  .home {
    padding: 0 16px;
  }
  .home.active {
    background: var(--bg);
    color: var(--text);
  }

  .session {
    min-width: 0;
    max-width: 220px;
    gap: 2px;
    padding-right: 6px;
  }
  .select {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    height: 100%;
    padding: 0 4px 0 12px;
    border: 0;
    background: transparent;
    color: inherit;
    font: inherit;
    cursor: default;
  }
  .session.active {
    background: var(--terminal);
    color: var(--text);
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-mono);
    font-size: 12px;
  }

  .dot {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: #6f7a87;
  }
  .dot.connecting {
    animation: pulse 1.2s ease-in-out infinite;
  }
  .dot.connected {
    background: var(--ok);
  }
  .dot.error {
    background: var(--error);
  }
  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .dot.connecting {
      animation: none;
    }
  }

  .close-tab,
  .plus {
    display: grid;
    place-items: center;
    flex: none;
    padding: 0;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    cursor: default;
    transition: background-color 80ms;
    --wails-draggable: no-drag;
  }
  .close-tab {
    width: 20px;
    height: 20px;
  }
  .plus {
    width: 28px;
    height: 28px;
    margin: 0 0 2px 4px;
    align-self: flex-end;
  }
  .close-tab:hover,
  .plus:hover {
    background: #262b33;
    color: var(--text);
  }

  .caption {
    display: flex;
    flex: none;
    --wails-draggable: no-drag;
  }

  .action {
    display: grid;
    place-items: center;
    width: 32px;
    height: 28px;
    margin: 6px 8px 0 0;
    padding: 0;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    cursor: default;
    transition: background-color 80ms;
  }
  .action:hover,
  .action.on {
    background: #262b33;
    color: var(--text);
  }

  .btn {
    display: grid;
    place-items: center;
    width: var(--caption-button-width);
    height: 100%;
    padding: 0;
    border: 0;
    border-radius: 0;
    background: transparent;
    color: var(--text);
    cursor: default;
    --wails-draggable: no-drag;
  }

  .btn:hover {
    background: var(--border);
  }

  .close:hover {
    background: var(--danger);
  }

  button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
</style>
