<script lang="ts">
  import { FitAddon } from '@xterm/addon-fit'
  import { SearchAddon } from '@xterm/addon-search'
  import { WebglAddon } from '@xterm/addon-webgl'
  import { Terminal, type ITheme } from '@xterm/xterm'
  import '@xterm/xterm/css/xterm.css'
  import { onMount } from 'svelte'
  import { EventsOn, SessionResize, SessionWrite } from '../api'
  import { registerTerminalFocus, sessions } from '../sessions.svelte'
  import { settings } from '../stores.svelte'
  import { decodeBase64 } from './base64'
  import { inputBytes, orderedSender } from './input'
  import { copyText, isAppShortcut, readClipboard, terminalAction } from './keys'
  import SearchBar from './SearchBar.svelte'

  let { sessionId, visible }: { sessionId: string; visible: boolean } = $props()

  const theme: ITheme = {
    background: '#0f1114',
    foreground: '#d8dde3',
    cursor: '#d8dde3',
    cursorAccent: '#0f1114',
    selectionBackground: 'rgba(79,143,247,.35)',
    black: '#1a1d22',
    red: '#f08c7d',
    green: '#7ee787',
    yellow: '#e3b341',
    blue: '#79c0ff',
    magenta: '#c9a3ff',
    cyan: '#7fdbdb',
    white: '#e8eaed',
    brightBlack: '#6f7a87',
    brightRed: '#f5a89c',
    brightGreen: '#a2f0a8',
    brightYellow: '#f0c966',
    brightBlue: '#a1d3ff',
    brightMagenta: '#dcc4ff',
    brightCyan: '#a3e8e8',
    brightWhite: '#ffffff',
  }

  const fontFamily = (name: string) => (name ? `"${name.replaceAll('"', '')}", ` : '') + 'Consolas, monospace'

  let host: HTMLDivElement
  let term = $state<Terminal>()
  let search = $state<SearchAddon>()
  let fit: FitAddon | undefined
  const searchOpen = $derived(sessions.searchId === sessionId)

  const send = orderedSender(
    (b64) => SessionWrite(sessionId, b64),
    (e) => console.error('SessionWrite', e),
  )

  function setSize(cols: number, rows: number): void {
    const tab = sessions.list.find((t) => t.id === sessionId)
    if (tab) Object.assign(tab, { cols, rows })
    void SessionResize(sessionId, cols, rows).catch(() => {})
  }

  /** Fits the terminal to its box; a hidden view (display:none) has no size and keeps its own. */
  function refit(): void {
    if (fit && host.clientWidth > 0 && host.clientHeight > 0) fit.fit()
  }

  async function paste(): Promise<void> {
    const text = await readClipboard().catch(() => '')
    if (text) term?.paste(text)
  }

  function toggleSearch(): void {
    sessions.searchId = searchOpen ? '' : sessionId
    if (!searchOpen) term?.focus()
  }

  onMount(() => {
    const t = new Terminal({
      theme,
      fontFamily: fontFamily(settings.terminal_font),
      fontSize: settings.terminal_size || 13,
      cursorBlink: settings.blink_cursor,
      allowProposedApi: true,
      scrollback: 10000,
    })
    fit = new FitAddon()
    const s = new SearchAddon()
    t.loadAddon(fit)
    t.loadAddon(s)
    t.open(host)

    t.attachCustomKeyEventHandler((e) => {
      if (!isAppShortcut(e)) return true
      const action = terminalAction(e)
      if (action === 'copy') void copyText(t.getSelection()).catch(() => {})
      if (action === 'paste') void paste()
      if (action === 'search') toggleSearch()
      if (action) e.preventDefault()
      return false // tab shortcuts bubble to the window handler in TitleBar
    })
    const subs = [
      t.onData((d) => send(inputBytes(d))),
      t.onBinary((d) => send(inputBytes(d, true))),
      t.onResize(({ cols, rows }) => setSize(cols, rows)),
    ]
    const offData = EventsOn('term:data:' + sessionId, (b64: string) => t.write(decodeBase64(b64)))

    term = t
    search = s
    refit()
    // The first resize also tells Go the view listens: held output is released after it.
    setSize(t.cols, t.rows)
    if (visible) t.focus()
    // The trust dialog hands focus back here, so the prompt after it gets the keystrokes.
    const offFocus = registerTerminalFocus(sessionId, () => t.focus())

    let timer: ReturnType<typeof setTimeout> | undefined
    const ro = new ResizeObserver(() => {
      clearTimeout(timer)
      timer = setTimeout(refit, 50)
    })
    ro.observe(host)

    return () => {
      clearTimeout(timer)
      ro.disconnect()
      offFocus()
      offData()
      for (const d of subs) d.dispose()
      disposeWebgl()
      t.dispose()
      term = undefined
    }
  })

  let webgl: WebglAddon | undefined

  function disposeWebgl(): void {
    webgl?.dispose()
    webgl = undefined
  }

  /** The WebGL renderer follows the setting live; without WebGL2 xterm keeps its DOM renderer. */
  $effect(() => {
    const t = term
    if (!t) return
    if (!settings.webgl) {
      disposeWebgl()
      return
    }
    if (webgl) return
    try {
      const gl = new WebglAddon()
      gl.onContextLoss(() => {
        if (webgl === gl) disposeWebgl() // falls back to the DOM renderer
      })
      t.loadAddon(gl)
      webgl = gl
    } catch {
      // No WebGL2 context.
    }
  })

  // Settings apply to open terminals at once.
  $effect(() => {
    if (!term) return
    term.options.fontFamily = fontFamily(settings.terminal_font)
    term.options.fontSize = settings.terminal_size || 13
    term.options.cursorBlink = settings.blink_cursor
    refit()
  })

  $effect(() => {
    if (visible) term?.focus()
  })

  function closeSearch(): void {
    sessions.searchId = ''
    term?.focus()
  }

  /** Copy-on-select copies once, when the mouse selection ends. */
  function onmouseup(e: MouseEvent): void {
    if (e.button === 0 && settings.copy_on_select && term?.hasSelection()) void copyText(term.getSelection()).catch(() => {})
  }

  function oncontextmenu(e: MouseEvent): void {
    e.preventDefault()
    void paste()
  }
</script>

<div class="terminal" {oncontextmenu} {onmouseup} role="presentation">
  <div class="xterm-host" bind:this={host}></div>
  {#if searchOpen && search}
    <SearchBar {search} onclose={closeSearch} />
  {/if}
</div>

<style>
  .terminal {
    position: relative;
    height: 100%;
    padding: 12px 18px;
    background: var(--terminal);
    user-select: text;
  }
  .xterm-host {
    width: 100%;
    height: 100%;
  }
</style>
