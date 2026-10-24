<script lang="ts">
  import { Quit, WindowMinimise, WindowToggleMaximise } from '../../../wailsjs/runtime/runtime'

  let { title = '' }: { title?: string } = $props()
</script>

<header class="titlebar" role="presentation" ondblclick={WindowToggleMaximise}>
  <div class="left">
    <svg class="glyph" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M3 4.5 7 8l-4 3.5" />
      <path d="M8.5 12H13" />
    </svg>
    {#if title}<span class="home">{title}</span>{/if}
  </div>
  <div class="caption">
    <button type="button" class="btn" aria-label="Свернуть" onclick={WindowMinimise} ondblclick={(e) => e.stopPropagation()}>
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1" aria-hidden="true">
        <path d="M0 5.5h10" />
      </svg>
    </button>
    <button type="button" class="btn" aria-label="Развернуть" onclick={WindowToggleMaximise} ondblclick={(e) => e.stopPropagation()}>
      <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1" aria-hidden="true">
        <rect x="0.5" y="0.5" width="9" height="9" />
      </svg>
    </button>
    <button type="button" class="btn close" aria-label="Закрыть" onclick={Quit} ondblclick={(e) => e.stopPropagation()}>
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
    background: var(--titlebar);
    color: var(--text);
    --wails-draggable: drag;
  }

  .left {
    display: flex;
    align-items: center;
    padding-left: 12px;
  }

  .home {
    display: flex;
    align-items: center;
    height: 100%;
    margin-left: 12px;
    padding: 0 16px;
    background: var(--bg);
    font-size: 13px;
  }

  .glyph {
    color: var(--accent);
  }

  .caption {
    display: flex;
    --wails-draggable: no-drag;
  }

  .btn {
    display: grid;
    place-items: center;
    width: var(--caption-button-width);
    height: var(--titlebar-height);
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
</style>
