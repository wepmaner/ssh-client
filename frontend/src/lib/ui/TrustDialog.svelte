<script lang="ts">
  import { onDestroy } from 'svelte'
  import { ClipboardSetText, errorText } from '../api'
  import { toast } from '../stores.svelte'
  import { answerTrust, hostPort, subscribeTrust, trust, trustTitle } from '../trust.svelte'
  import Button from './Button.svelte'
  import Dialog from './Dialog.svelte'

  /** The app-modal question about an unknown server key; mounted once, above every other dialog. */
  onDestroy(subscribeTrust())

  const current = $derived(trust.queue.slice(0, 1))

  // Esc cancels this dialog only: caught before the window listeners of the dialogs below it.
  function onkeydown(e: KeyboardEvent): void {
    const q = trust.queue[0]
    if (!q || e.key !== 'Escape') return
    e.preventDefault()
    e.stopImmediatePropagation()
    void answerTrust(q.id, false)
  }

  async function copy(text: string): Promise<void> {
    try {
      await ClipboardSetText(text)
      toast('Отпечаток скопирован')
    } catch (e) {
      toast(errorText(e))
    }
  }
</script>

<svelte:window onkeydowncapture={onkeydown} />

<!-- Keyed by question: the next one in the queue opens as a fresh dialog with its own focus. -->
{#each current as q (q.id)}
  <Dialog open title={trustTitle} onclose={() => answerTrust(q.id, false)} layer={15} autofocus="[data-cancel]">
    <p class="target">{q.user}@{hostPort(q.host, q.port)}</p>
    <div class="label">Ключ {q.key_type}, отпечаток SHA256</div>
    <div class="fp">
      <code>{q.fingerprint}</code>
      <button type="button" class="copy" title="Скопировать" aria-label="Скопировать" onclick={() => copy(q.fingerprint)}>
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" aria-hidden="true">
          <rect x="4.5" y="4.5" width="8" height="8" rx="1.5" />
          <path d="M9.5 2.5v-.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h.5" />
        </svg>
      </button>
    </div>
    <p class="note">
      Клиент видит этот сервер впервые. Если отпечаток совпадает с тем, что дал администратор, подключение безопасно.
    </p>
    {#snippet footer()}
      <span></span>
      <span class="actions">
        <!-- The safe answer has the focus: Enter on an unread prompt must not trust a server. -->
        <Button variant="outline" data-cancel onclick={() => answerTrust(q.id, false)}>Отмена</Button>
        <Button variant="primary" onclick={() => answerTrust(q.id, true)}>Доверять и подключиться</Button>
      </span>
    {/snippet}
  </Dialog>
{/each}

<style>
  .target {
    margin: 0 0 16px;
    font-family: var(--font-mono);
    font-size: 13px;
    overflow-wrap: anywhere;
  }
  .label {
    margin-bottom: 6px;
    color: var(--muted);
    font-size: 12px;
  }
  .fp {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 6px 6px 10px;
    background: var(--terminal);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
  }
  code {
    flex: 1;
    min-width: 0;
    font-family: var(--font-mono);
    font-size: 12.5px;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .copy {
    display: grid;
    place-items: center;
    flex: none;
    width: 28px;
    height: 28px;
    padding: 0;
    border: none;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    cursor: default;
    transition: background-color 80ms;
  }
  .copy:hover {
    background: #262b33;
    color: var(--text);
  }
  .copy:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  .note {
    margin: 16px 0 0;
    color: var(--muted);
    line-height: 1.45;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
