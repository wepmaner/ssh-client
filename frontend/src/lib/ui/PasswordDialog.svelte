<script lang="ts">
  import { errorText } from '../api'
  import Button from './Button.svelte'
  import Dialog from './Dialog.svelte'
  import Input from './Input.svelte'

  interface Props {
    open: boolean
    title: string
    hint: string
    confirmLabel: string
    /** Rejects with the error to show; resolving closes the dialog. */
    onsubmit: (password: string) => Promise<void>
    onclose: () => void
  }
  let { open, title, hint, confirmLabel, onsubmit, onclose }: Props = $props()

  let pw = $state('')
  let error = $state('')
  let busy = $state(false)

  $effect(() => {
    if (!open) return
    pw = ''
    error = ''
    busy = false
  })

  async function submit() {
    if (busy) return
    busy = true
    error = ''
    try {
      await onsubmit(pw)
      onclose()
    } catch (e) {
      error = errorText(e)
    } finally {
      busy = false
    }
  }
</script>

<Dialog {open} {title} {onclose} width={400}>
  <form
    id="password-form"
    onsubmit={(e) => {
      e.preventDefault()
      submit()
    }}
  >
    <p class="hint">{hint}</p>
    <label>
      <span>Пароль хранилища</span>
      <Input type="password" bind:value={pw} autocomplete="current-password" />
    </label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <span></span>
    <span class="actions">
      <Button variant="ghost" onclick={onclose}>Отмена</Button>
      <Button variant="primary" type="submit" form="password-form" disabled={!pw || busy}>{confirmLabel}</Button>
    </span>
  {/snippet}
</Dialog>

<style>
  .hint {
    margin: 0 0 14px;
    color: var(--muted);
    line-height: 1.45;
  }
  label span {
    display: block;
    margin-bottom: 6px;
    color: var(--muted);
    font-size: 12px;
  }
  .error {
    margin: 10px 0 0;
    color: var(--error);
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
