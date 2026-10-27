<script lang="ts">
  import { VaultResetCorrupt, errorText } from '../api'
  import RestoreButton from '../backup/RestoreButton.svelte'
  import Button from '../ui/Button.svelte'

  let { error, onreset }: { error: string; onreset: () => void } = $props()

  let failure = $state('')
  let busy = $state(false)

  async function reset() {
    busy = true
    failure = ''
    try {
      await VaultResetCorrupt()
      onreset()
    } catch (e) {
      failure = errorText(e)
    } finally {
      busy = false
    }
  }
</script>

<main>
  <div class="box">
    <h1>Хранилище повреждено</h1>
    <p>{error}</p>
    <p class="note">Старый файл будет сохранён как vault.json.corrupt</p>
    {#if failure}<p class="fail" role="alert">{failure}</p>{/if}
    <div class="actions">
      <Button variant="primary" disabled={busy} onclick={reset}>Начать заново</Button>
      <RestoreButton disabled={busy} />
    </div>
  </div>
</main>

<style>
  main {
    flex: 1;
    display: grid;
    place-items: center;
    background: var(--bg);
  }
  .box {
    width: 360px;
    text-align: center;
  }
  h1 {
    margin: 0 0 12px;
    font-size: 18px;
    font-weight: 600;
  }
  p {
    margin: 0 0 8px;
    line-height: 1.45;
  }
  .note {
    margin-bottom: 20px;
    color: var(--muted);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 8px;
  }
  .fail {
    color: var(--error);
  }
</style>
