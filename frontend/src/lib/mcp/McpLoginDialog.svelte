<script lang="ts">
  import { onDestroy } from 'svelte'
  import { EventsOn, MCPLoginAttach, MCPLoginWrite } from '../api'
  import MiniTerminal from '../forwards/MiniTerminal.svelte'
  import { decodeBase64 } from '../terminal/base64'
  import Dialog from '../ui/Dialog.svelte'
  import { cancelMcpLogin, mcpLoginTitle, mcpLogins, subscribeMcpLogins, type McpLogin } from './mcp.svelte'

  /** The login of the MCP server to a host: its prompts are answered here. Mounted once. */
  onDestroy(subscribeMcpLogins())

  const current = $derived(mcpLogins.queue.slice(0, 1))

  // Esc cancels the login even with focus in the terminal: caught in the capture phase, before xterm
  // sees it, so  never reaches the server.
  function onkeydown(e: KeyboardEvent): void {
    const l = mcpLogins.queue[0]
    if (!l || e.key !== 'Escape') return
    e.preventDefault()
    e.stopImmediatePropagation()
    void cancelMcpLogin(l)
  }

  /** Output of the login: listen first, then tell Go, or the questions it holds stay held. */
  function attachTo(l: McpLogin): (sink: (b: Uint8Array) => void) => () => void {
    return (sink) => {
      const off = EventsOn('mcplogin:data:' + l.host_id, (b64: string) => sink(decodeBase64(b64)))
      void MCPLoginAttach(l.host_id, l.login_id).catch(() => {})
      return off
    }
  }
</script>

<svelte:window onkeydowncapture={onkeydown} />

<!-- Keyed by login: the next one in the queue opens as a fresh dialog with a fresh terminal. -->
{#each current as l (l.login_id)}
  <Dialog open title={mcpLoginTitle(l.host_name)} onclose={() => cancelMcpLogin(l)} layer={12}>
    <MiniTerminal write={(b64) => MCPLoginWrite(l.host_id, l.login_id, b64)} attach={attachTo(l)} />
  </Dialog>
{/each}
