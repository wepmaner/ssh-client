<script lang="ts">
  import { VaultCounts, VaultSetPassword, errorText } from '../api'
  import { passwordStrength, strengthLabel } from '../password'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'

  interface Props {
    open: boolean
    onclose: () => void
    onenabled?: () => void
  }
  let { open, onclose, onenabled }: Props = $props()

  let pw = $state('')
  let again = $state('')
  let counts = $state({ keys: 0, hosts: 0 })
  let error = $state('')
  let busy = $state(false)

  const strength = $derived(passwordStrength(pw))
  const match = $derived(pw === again)
  const valid = $derived(pw.length >= 8 && match)

  $effect(() => {
    if (!open) return
    pw = ''
    again = ''
    error = ''
    VaultCounts()
      .then((c) => (counts = c))
      .catch(() => {})
  })

  async function submit() {
    if (!valid || busy) return
    busy = true
    error = ''
    try {
      await VaultSetPassword(pw)
      onenabled?.()
      onclose()
    } catch (e) {
      error = errorText(e)
    } finally {
      busy = false
    }
  }
</script>

<Dialog {open} title="Шифрование хранилища" {onclose}>
  <form
    id="vault-form"
    onsubmit={(e) => {
      e.preventDefault()
      submit()
    }}
  >
    <p class="intro">
      Ключи, хосты и пробросы будут зашифрованы паролем. Пароль спрашивается один раз при запуске приложения.
    </p>
    <div class="fields">
      <label>
        <span>Пароль</span>
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
    <div class="warn" role="note">
      Пароль нельзя восстановить. Если его забыть, ключи и хосты будут потеряны. Заранее сохраните публичные ключи на
      серверах.
    </div>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <span class="count">Будет зашифровано: {counts.keys} ключей, {counts.hosts} хостов</span>
    <span class="actions">
      <Button variant="ghost" onclick={onclose}>Отмена</Button>
      <Button variant="primary" type="submit" form="vault-form" disabled={!valid || busy}>Включить шифрование</Button>
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
  .count {
    color: var(--muted);
    font-size: 12px;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
