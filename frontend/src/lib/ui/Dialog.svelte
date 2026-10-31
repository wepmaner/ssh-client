<script lang="ts">
  import { tick, type Snippet } from 'svelte'
  import Button from './Button.svelte'

  interface Props {
    open: boolean
    title: string
    onclose: () => void
    children: Snippet
    footer?: Snippet
    width?: number
    /** Stacking level of the scrim; an app-modal dialog sits above the others. */
    layer?: number
    /** What gets focus on open; by default the first field. */
    autofocus?: string
    /** Give focus back to what had it before the dialog opened; off for callers that do it themselves. */
    restoreFocus?: boolean
  }
  let {
    open,
    title,
    onclose,
    children,
    footer,
    width = 500,
    layer = 10,
    autofocus = 'input, [role="combobox"], textarea',
    restoreFocus = true,
  }: Props = $props()

  let panel: HTMLDivElement | undefined = $state()

  // Move focus into the first field once the panel is mounted; on close, hand it back to the
  // element that had it, unless that left the page or the closing code focused something else.
  $effect(() => {
    if (!open || !panel) return
    const opener = document.activeElement
    const self = panel
    self.querySelector<HTMLElement>(autofocus)?.focus()
    return () => {
      if (!restoreFocus) return
      void tick().then(() => {
        const now = document.activeElement
        const lost = now === null || now === document.body || self.contains(now)
        if (lost && opener instanceof HTMLElement && opener.isConnected && opener !== document.body) opener.focus()
      })
    }
  })

  function onkeydown(e: KeyboardEvent) {
    if (open && e.key === 'Escape') {
      e.preventDefault()
      onclose()
    }
  }
</script>

<svelte:window {onkeydown} />

{#if open}
  <div class="scrim" role="presentation" style:z-index={layer} onmousedown={(e) => e.target === e.currentTarget && onclose()}>
    <div class="panel" role="dialog" aria-modal="true" aria-label={title} bind:this={panel} style:width="{width}px">
      <header>
        <h2>{title}</h2>
        <Button variant="ghost" aria-label="Закрыть" onclick={onclose} style="padding:0;width:32px">
          <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" aria-hidden="true">
            <path d="M0 0l10 10M10 0 0 10" />
          </svg>
        </Button>
      </header>
      <div class="body">{@render children()}</div>
      {#if footer}<footer>{@render footer()}</footer>{/if}
    </div>
  </div>
{/if}

<style>
  .scrim {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: rgba(8, 10, 14, 0.62);
    z-index: 10;
  }
  .panel {
    width: 500px;
    max-width: calc(100vw - 32px);
    background: #1e2228;
    border: 1px solid #333a44;
    border-radius: var(--radius-dialog);
    box-shadow: 0 24px 64px rgba(0, 0, 0, 0.55);
    animation: enter 150ms ease-out;
  }
  @keyframes enter {
    from {
      opacity: 0;
      transform: scale(0.97);
    }
    to {
      opacity: 1;
      transform: scale(1);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .panel {
      animation: none;
    }
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 50px;
    padding: 0 10px 0 20px;
    border-bottom: 1px solid #2a2f37;
  }
  h2 {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
  }
  .body {
    padding: 20px;
  }
  footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 20px;
    border-top: 1px solid #2a2f37;
  }
</style>
