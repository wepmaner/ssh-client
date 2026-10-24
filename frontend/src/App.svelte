<script lang="ts">
  import { onMount } from 'svelte'
  import KeysView from './lib/keys/KeysView.svelte'
  import CorruptScreen from './lib/settings/CorruptScreen.svelte'
  import UnlockScreen from './lib/settings/UnlockScreen.svelte'
  import Sidebar from './lib/sidebar/Sidebar.svelte'
  import { refreshAll, ui, vault, type View } from './lib/stores.svelte'
  import TitleBar from './lib/titlebar/TitleBar.svelte'
  import Toast from './lib/ui/Toast.svelte'

  const titles: Record<View, string> = {
    hosts: 'Хосты',
    keys: 'Ключи',
    forwards: 'Пробросы',
    settings: 'Настройки',
  }
  const shell = $derived(vault.ready && !vault.corrupt && !vault.locked)

  onMount(refreshAll)
</script>

<div class="window">
  <TitleBar title={shell ? titles[ui.view] : ''} />
  {#if !vault.ready}
    <div class="fill"></div>
  {:else if vault.corrupt}
    <CorruptScreen error={vault.error} onreset={refreshAll} />
  {:else if vault.locked}
    <UnlockScreen onunlocked={refreshAll} />
  {:else}
    <div class="shell">
      <Sidebar />
      <main class="fill">
        {#if ui.view === 'keys'}
          <KeysView />
        {:else}
          <h1 class="placeholder">{titles[ui.view]}</h1>
        {/if}
      </main>
    </div>
  {/if}
  <Toast />
</div>

<style>
  .window {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  .shell {
    display: flex;
    flex: 1;
    min-height: 0;
  }
  .fill {
    flex: 1;
    min-width: 0;
  }
  .placeholder {
    margin: 24px;
    font-size: 16px;
    font-weight: 600;
  }
</style>
