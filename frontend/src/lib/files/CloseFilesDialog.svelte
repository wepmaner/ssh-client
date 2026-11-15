<script lang="ts">
  import { closeSession, sessions } from '../sessions.svelte'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'

  /** Asks before a file tab with transfers under way is closed. Mounted once. */
  const tab = $derived(sessions.list.find((t) => t.id === sessions.closeAsk))

  function cancel(): void {
    sessions.closeAsk = ''
  }

  function confirm(): void {
    const id = sessions.closeAsk
    sessions.closeAsk = ''
    if (id) closeSession(id)
  }
</script>

<Dialog open={!!tab} title="Остановить передачи и закрыть?" onclose={cancel} width={440} autofocus="button.btn.primary">
  <p>Передачи вкладки «{tab?.hostName}» будут отменены, недописанные файлы удалены.</p>
  {#snippet footer()}
    <span></span>
    <span class="actions">
      <Button variant="ghost" onclick={cancel}>Отмена</Button>
      <Button variant="primary" onclick={confirm}>Остановить и закрыть</Button>
    </span>
  {/snippet}
</Dialog>

<style>
  p {
    margin: 0;
    color: var(--muted);
    line-height: 1.45;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
