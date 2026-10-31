<script lang="ts">
  import { MCPJournalClear, MCPJournalList, errorText, type model } from '../api'
  import { toast } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'

  let { open, onclose }: { open: boolean; onclose: () => void } = $props()

  let records = $state<model.MCPRecord[]>([])

  async function load(): Promise<void> {
    try {
      // Go returns it newest first.
      records = (await MCPJournalList()) ?? []
    } catch (e) {
      toast(errorText(e))
    }
  }

  $effect(() => {
    if (open) void load()
  })

  async function clear(): Promise<void> {
    try {
      await MCPJournalClear()
      await load()
    } catch (e) {
      toast(errorText(e))
    }
  }

  const accessLabel: Record<string, string> = { read: 'Только просмотр', full: 'Полный' }

  function when(t: unknown): string {
    const d = new Date(t as string)
    return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('ru-RU')
  }
</script>

<Dialog {open} title="Журнал MCP" {onclose} width={640}>
  {#if records.length === 0}
    <p class="empty">Записей нет</p>
  {:else}
    <ul class="list">
      {#each records as r (r.id)}
        <li class="rec">
          <div class="meta">
            <span>{when(r.time)}</span>
            <span class="host">{r.host_name}</span>
            <span class="tool">{r.tool}</span>
            <span>{accessLabel[r.access] ?? ''}</span>
          </div>
          <code>{r.text}</code>
          {#if r.refused}
            <span class="refused">{r.refused}</span>
          {:else if r.exit_code != null}
            <span class="result">Код выхода {r.exit_code}{r.duration_ms ? `, ${(r.duration_ms / 1000).toFixed(1)} с` : ''}</span>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#snippet footer()}
    <span></span>
    <Button variant="outline" disabled={records.length === 0} onclick={clear}>Очистить</Button>
  {/snippet}
</Dialog>

<style>
  .empty {
    margin: 0;
    color: var(--muted);
  }
  .list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    max-height: 56vh;
    margin: 0;
    padding: 0;
    overflow: auto;
    list-style: none;
  }
  .rec {
    display: flex;
    flex-direction: column;
    gap: 3px;
    padding: 8px 10px;
    background: var(--terminal);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
  }
  .meta {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
    color: var(--muted);
    font-size: 12px;
  }
  .host {
    color: var(--text);
  }
  code {
    font-family: var(--font-mono);
    font-size: 12.5px;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .refused {
    color: var(--error);
    font-size: 12px;
  }
  .result {
    color: var(--muted);
    font-size: 12px;
  }
</style>
