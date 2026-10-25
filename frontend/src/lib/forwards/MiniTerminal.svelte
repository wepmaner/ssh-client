<script lang="ts">
  import { FitAddon } from '@xterm/addon-fit'
  import { Terminal } from '@xterm/xterm'
  import '@xterm/xterm/css/xterm.css'
  import { onMount } from 'svelte'
  import { ForwardWrite } from '../api'
  import { settings } from '../stores.svelte'
  import { inputBytes, orderedSender } from '../terminal/input'
  import { copyText, isAppShortcut, readClipboard, terminalAction } from '../terminal/keys'
  import { attachOutput } from './forwards.svelte'

  /** A six-row terminal on a forward card: the dial's prompts are answered here. */
  let { forwardId }: { forwardId: string } = $props()

  const ROWS = 6
  let host: HTMLDivElement
  let term: Terminal | undefined

  const send = orderedSender(
    (b64) => ForwardWrite(forwardId, b64),
    (e) => console.error('ForwardWrite', e),
  )

  function paste(): void {
    void readClipboard()
      .then((text) => text && term?.paste(text))
      .catch(() => {})
  }

  onMount(() => {
    const t = new Terminal({
      rows: ROWS,
      theme: { background: '#0f1114', foreground: '#d8dde3', cursor: '#d8dde3', cursorAccent: '#0f1114' },
      fontFamily: (settings.terminal_font ? `"${settings.terminal_font.replaceAll('"', '')}", ` : '') + 'Consolas, monospace',
      fontSize: 12,
      scrollback: 200,
    })
    const fit = new FitAddon()
    t.loadAddon(fit)
    t.open(host)
    // Ctrl+Shift+C / Ctrl+Shift+V as in the session terminal; tab shortcuts bubble to the window.
    t.attachCustomKeyEventHandler((e) => {
      if (!isAppShortcut(e)) return true
      const action = terminalAction(e)
      if (action === 'copy') void copyText(t.getSelection()).catch(() => {})
      if (action === 'paste') paste()
      if (action) e.preventDefault()
      return false
    })
    const sub = t.onData((d) => send(inputBytes(d)))
    const detach = attachOutput(forwardId, (b) => t.write(b))

    // Only the width follows the card; the height stays six rows.
    const fitWidth = () => {
      const cols = fit.proposeDimensions()?.cols
      if (cols && cols > 0 && cols !== t.cols) t.resize(cols, ROWS)
    }
    const ro = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(fitWidth)
    ro?.observe(host)
    fitWidth()
    t.focus()
    term = t

    return () => {
      term = undefined
      ro?.disconnect()
      detach()
      sub.dispose()
      t.dispose()
    }
  })
</script>

<!-- Right click pastes, as in the session terminal. It stops there: the card's menu
     must not open over the prompt. -->
<div
  class="mini"
  data-testid="mini-terminal"
  role="presentation"
  oncontextmenu={(e) => {
    e.preventDefault()
    e.stopPropagation()
    paste()
  }}
>
  <div class="xterm-host" bind:this={host}></div>
</div>

<style>
  .mini {
    padding: 6px 8px;
    background: var(--terminal);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    user-select: text;
  }
  .xterm-host {
    width: 100%;
  }
</style>
