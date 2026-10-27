<script lang="ts">
  import { VaultBackup, errorText } from '../api'
  import { passwordStrength, strengthLabel } from '../password'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'

  interface Props {
    open: boolean
    onclose: () => void
    /** The copy was written to path. */
    onsaved: (path: string) => void
  }
  let { open, onclose, onsaved }: Props = $props()

  let pw = $state('')
  let again = $state('')
  let error = $state('')
  let busy = $state(false)

  const strength = $derived(passwordStrength(pw))
  const match = $derived(pw === again)
  const valid = $derived([...pw].length >= 8 && match)

  $effect(() => {
    if (!open) return
    pw = ''
    again = ''
    error = ''
    busy = false
  })

  async function submit() {
    if (!valid || busy) return
    busy = true
    error = ''
    try {
      const path = await VaultBackup(pw)
      if (!path) return // the save dialog was cancelled: the password stays for another try
      onsaved(path)
      onclose()
    } catch (e) {
      error = errorText(e)
    } finally {
      busy = false
    }
  }
</script>

<Dialog {open} title="Резервная копия" {onclose}>
  <form
    id="backup-form"
    onsubmit={(e) => {
      e.preventDefault()
      submit()
    }}
  >
    <p class="intro">
      Копия ключей, хостов и пробросов будет зашифрована этим паролем и откроется на любом компьютере. Само хранилище
      по-прежнему защищено учётной записью Windows.
    </p>
    <div class="fields">
      <label>
        <span>Пароль копии</span>
        <Input type="password" bind:value={pw} autocomplete="new-password" />
      </label>
      <label>
        <span>Ещё раз</span>
        <Input type="password" bind:value={again} autocomplete="new-password" />
      </label>
    </div>
    <div class="meter" aria-hidden="true">
      {#each [0, 1, 2, 3] as i (i)}
        <i class:on={i < strength}></i>
      {/each}
    </div>
    <p class="caption">
      {#if pw}{strengthLabel(strength)}{:else}&nbsp;{/if}{#if again}{pw ? ' · ' : ''}{match
          ? 'пароли совпадают'
          : 'пароли не совпадают'}{/if}
    </p>
    <div class="warn" role="note">Пароль копии нельзя восстановить. Без него копию не открыть.</div>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <span></span>
    <span class="actions">
      <Button variant="ghost" onclick={onclose}>Отмена</Button>
      <Button variant="primary" type="submit" form="backup-form" disabled={!valid || busy}>Сохранить копию</Button>
    </span>
  {/snippet}
</Dialog>

<style>
  .intro {
    margin: 0 0 16px;
    color: var(--muted);
    line-height: 1.45;
  }
  .fields {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  label span {
    display: block;
    margin-bottom: 6px;
    color: var(--muted);
    font-size: 12px;
  }
  .meter {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 4px;
    margin-top: 14px;
  }
  .meter i {
    height: 3px;
    border-radius: 2px;
    background: #2e333b;
  }
  .meter i.on {
    background: #3fb950;
  }
  .caption {
    margin: 6px 0 16px;
    color: var(--muted);
    font-size: 12px;
  }
  .warn {
    padding: 10px 12px;
    background: #2b2420;
    border: 1px solid #4a3528;
    border-radius: var(--radius-control);
    color: #e8c9b8;
    line-height: 1.45;
  }
  .error {
    margin: 12px 0 0;
    color: var(--error);
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
