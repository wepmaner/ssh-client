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

  /** Output of the login: listen first, then tell Go, or the questions it holds stay held. */
  function attachTo(l: McpLogin): (sink: (b: Uint8Array) => void) => () => void {
    return (sink) => {
      const off = EventsOn('mcplogin:data:' + l.host_id, (b64: string) => sink(decodeBase64(b64)))
      void MCPLoginAttach(l.host_id, l.login_id).catch(() => {})
      return off
    }
  }
</script>

<!-- Keyed by login: the next one in the queue opens as a fresh dialog with a fresh terminal. -->
{#each current as l (l.login_id)}
  <Dialog open title={mcpLoginTitle(l.host_name)} onclose={() => cancelMcpLogin(l)} layer={12}>
    <MiniTerminal write={(b64) => MCPLoginWrite(l.host_id, l.login_id, b64)} attach={attachTo(l)} />
  </Dialog>
{/each}
