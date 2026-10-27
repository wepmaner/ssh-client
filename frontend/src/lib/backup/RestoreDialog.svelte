<script lang="ts">
  import { VaultCounts, VaultRestore, errorText } from '../api'
  import { fieldError } from '../hosts/format'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'

  interface Props {
    /** The backup file chosen in the system dialog; the dialog is open while it is set. */
    path: string
    onclose: () => void
    onrestored: () => void
  }
  let { path, onclose, onrestored }: Props = $props()

  let pw = $state('')
  let pwError = $state('')
  let error = $state('')
  let busy = $state(false)
  let replace = $state(false)

  const open = $derived(path !== '')
  const fileName = $derived(path.split(/[\\/]/).pop() ?? '')

  $effect(() => {
    if (!open) return
    pw = ''
    pwError = ''
    error = ''
    busy = false
    replace = false
    VaultCounts()
      .then((c) => (replace = c.keys + c.hosts + c.forwards > 0))
      .catch(() => {})
  })

  async function submit() {
    if (!pw || busy) return
    busy = true
    pwError = ''
    error = ''
    try {
      await VaultRestore(path, pw)
      onclose()
      onrestored()
    } catch (e) {
      const msg = errorText(e)
      const fe = fieldError(msg)
      if (fe) pwError = fe.text
      else error = msg
    } finally {
      busy = false
    }
  }
</script>

<Dialog {open} title="Восстановить из копии" {onclose} width={440}>
  <form
    id="restore-form"
    onsubmit={(e) => {
      e.preventDefault()
      submit()
    }}
  >
    <p class="hint">Файл <span class="file">{fileName}</span>. Введите пароль, которым зашифрована копия.</p>
    <label>
      <span>Пароль копии</span>
      <Input
        type="password"
        bind:value={pw}
        autocomplete="current-password"
        aria-invalid={pwError ? 'true' : undefined}
        aria-describedby={pwError ? 'restore-pw-error' : undefined}
      />
    </label>
    {#if pwError}<p class="field-error" id="restore-pw-error" role="alert">{pwError}</p>{/if}
    {#if replace}
      <div class="warn" role="note">
        Текущие ключи, хосты и пробросы будут заменены копией. Текущее хранилище сохранится рядом как
        vault.before-restore-&lt;дата&gt;.json.
      </div>
    {/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <span></span>
    <span class="actions">
      <Button variant="ghost" onclick={onclose}>Отмена</Button>
      <Button variant="primary" type="submit" form="restore-form" disabled={!pw || busy}>
        {replace ? 'Заменить' : 'Восстановить'}
      </Button>
    </span>
  {/snippet}
</Dialog>

<style>
  .hint {
    margin: 0 0 14px;
    color: var(--muted);
    line-height: 1.45;
  }
  .file {
    color: var(--text);
    overflow-wrap: anywhere;
  }
  label span {
    display: block;
    margin-bottom: 6px;
    color: var(--muted);
    font-size: 12px;
  }
  .field-error {
    margin: 6px 0 0;
    color: var(--error);
    font-size: 12px;
  }
  .warn {
    margin-top: 14px;
    padding: 10px 12px;
    background: #2b2420;
    border: 1px solid #4a3528;
    border-radius: var(--radius-control);
    color: #e8c9b8;
    line-height: 1.45;
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
