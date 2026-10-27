<script lang="ts">
  import { onMount } from 'svelte'
  import { ForwardDelete, errorText, type app } from '../api'
  import { toast } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import ForwardGroup from './ForwardGroup.svelte'
  import ForwardDialog from './ForwardDialog.svelte'
  import { forwards, refreshForwards } from './forwards.svelte'

  let dialog = $state<{ open: boolean; forward: app.ForwardDTO | null; template: app.ForwardDTO | null; hostId: string }>({ open: false, forward: null, template: null, hostId: '' })
  let confirm = $state<app.ForwardDTO | null>(null)

  const name = (a: { name: string }, b: { name: string }) => a.name.localeCompare(b.name)
  const groups = $derived(
    [...forwards.hosts]
      .sort((a, b) => a.host_name.localeCompare(b.host_name))
      .map((host) => ({ host, items: forwards.list.filter((f) => f.host_id === host.host_id).sort(name) })),
  )

  const running = $derived(forwards.list.filter((f) => f.status === 'running').length)
  const summary = $derived(`${running} из ${forwards.list.length} работают`)

  // Host names and states may have changed while another screen was in front.
  onMount(() => void refreshForwards().catch(() => {}))

  async function remove() {
    const f = confirm
    confirm = null
    if (!f) return
    try {
      await ForwardDelete(f.id)
    } catch (e) {
      toast(errorText(e))
    } finally {
      await refreshForwards().catch(() => {})
    }
  }
</script>

{#snippet plus()}
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
    <path d="M8 3v10M3 8h10" />
  </svg>
{/snippet}

<div class="view">
  {#if forwards.list.length === 0}
    <div class="empty">
      <h1>Пробросов пока нет</h1>
      <p>Проброс открывает порт через SSH-сервер и работает без открытого терминала.</p>
      <Button variant="primary" onclick={() => (dialog = { open: true, forward: null, template: null, hostId: '' })}>
        <span class="with-icon">{@render plus()}Новый проброс</span>
      </Button>
    </div>
  {:else}
    <div class="toolbar">
      <h1>Пробросы</h1>
      <span class="hint">{summary}</span>
      <span class="spacer"></span>
      <Button variant="primary" onclick={() => (dialog = { open: true, forward: null, template: null, hostId: '' })}>
        <span class="with-icon">{@render plus()}Новый проброс</span>
      </Button>
    </div>

    <div class="groups">
      {#each groups as g (g.host.host_id)}
        <ForwardGroup
          host={g.host}
          items={g.items}
          onadd={() => (dialog = { open: true, forward: null, template: null, hostId: g.host.host_id })}
          onedit={(f) => (dialog = { open: true, forward: f, template: null, hostId: '' })}
          onduplicate={(f) => (dialog = { open: true, forward: null, template: f, hostId: '' })}
          ondelete={(f) => (confirm = f)}
        />
      {/each}
    </div>

    <dl class="legend">
      <div><dt>L</dt><dd>порт на этом компьютере ведёт на адрес за сервером</dd></div>
      <div><dt>R</dt><dd>порт на сервере ведёт сюда</dd></div>
      <div><dt>D</dt><dd>SOCKS5-прокси для браузера и программ</dd></div>
    </dl>
  {/if}
</div>

<ForwardDialog open={dialog.open} forward={dialog.forward} template={dialog.template} hostId={dialog.hostId} onclose={() => (dialog = { open: false, forward: null, template: null, hostId: '' })} />

<Dialog open={confirm !== null} title={`Удалить проброс ${confirm?.name ?? ''}?`} onclose={() => (confirm = null)} width={420}>
  <p class="muted">Проброс остановится и будет удалён из хранилища.</p>
  {#snippet footer()}
    <span></span>
    <span class="actions">
      <Button variant="ghost" onclick={() => (confirm = null)}>Отмена</Button>
      <Button variant="primary" onclick={remove}>Удалить</Button>
    </span>
  {/snippet}
</Dialog>

<style>
  .view {
    display: flex;
    flex-direction: column;
    gap: 16px;
    height: 100%;
    padding: 14px 24px 32px;
    overflow: auto;
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
  }
  .with-icon {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  .spacer {
    flex: 1;
  }
  .toolbar h1 {
    margin: 0;
    font-size: 20px;
    font-weight: 600;
  }
  .hint {
    color: var(--muted);
    font-size: 13px;
  }
  .groups {
    display: flex;
    flex-direction: column;
    gap: 20px;
  }
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 20px;
    margin: 0;
    color: var(--muted);
    font-size: 11.5px;
  }
  .legend div {
    display: flex;
    gap: 6px;
  }
  .legend dt {
    font-family: var(--font-mono);
    font-weight: 500;
  }
  .legend div:nth-child(1) dt {
    color: #7fb0ff;
  }
  .legend div:nth-child(2) dt {
    color: #e3b341;
  }
  .legend div:nth-child(3) dt {
    color: #c9a3ff;
  }
  .legend dd {
    margin: 0;
  }
  .empty {
    display: flex;
    flex: 1;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 14px;
    text-align: center;
  }
  .empty h1 {
    margin: 0;
    font-size: 20px;
    font-weight: 600;
  }
  .empty p {
    max-width: 420px;
    margin: 0;
    color: var(--muted);
    font-size: 13.5px;
    line-height: 1.55;
  }
  .muted {
    margin: 0;
    color: var(--muted);
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
