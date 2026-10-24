<script lang="ts">
  import { VaultUnlock, errorText } from '../api'
  import Button from '../ui/Button.svelte'
  import Input from '../ui/Input.svelte'

  let { onunlocked }: { onunlocked: () => void } = $props()

  let pw = $state('')
  let error = $state('')
  let busy = $state(false)
  let field: HTMLInputElement | undefined = $state()

  $effect(() => {
    field?.focus()
  })

  async function submit() {
    if (busy) return
    busy = true
    error = ''
    try {
      await VaultUnlock(pw)
      onunlocked()
    } catch (e) {
      error = errorText(e)
      pw = ''
    } finally {
      busy = false
      field?.focus()
    }
  }
</script>

<main>
  <form
    onsubmit={(e) => {
      e.preventDefault()
      submit()
    }}
  >
    <svg class="glyph" width="40" height="40" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M3 4.5 7 8l-4 3.5" />
      <path d="M8.5 12H13" />
    </svg>
    <h1>Хранилище зашифровано</h1>
    <Input type="password" bind:value={pw} bind:el={field} placeholder="Пароль" aria-label="Пароль" autocomplete="current-password" />
    <p class="error" role="alert">{error}</p>
    <Button variant="primary" type="submit" disabled={busy} style="width:100%">Разблокировать</Button>
  </form>
</main>

<style>
  main {
    flex: 1;
    display: grid;
    place-items: center;
    background: var(--bg);
  }
  form {
    width: 320px;
    display: flex;
    flex-direction: column;
    align-items: stretch;
    text-align: center;
  }
  .glyph {
    align-self: center;
    color: var(--accent);
  }
  h1 {
    margin: 12px 0 20px;
    font-size: 18px;
    font-weight: 600;
  }
  .error {
    min-height: 20px;
    margin: 6px 0 10px;
    text-align: left;
    color: var(--error);
    font-size: 12px;
  }
</style>
