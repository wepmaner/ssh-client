<script lang="ts">
  import { onMount } from 'svelte'
  import CorruptScreen from './lib/settings/CorruptScreen.svelte'
  import UnlockScreen from './lib/settings/UnlockScreen.svelte'
  import { refreshAll, vault } from './lib/stores.svelte'
  import TitleBar from './lib/titlebar/TitleBar.svelte'

  onMount(refreshAll)
</script>

<div class="window">
  <TitleBar />
  {#if !vault.ready}
    <div class="fill"></div>
  {:else if vault.corrupt}
    <CorruptScreen error={vault.error} onreset={refreshAll} />
  {:else if vault.locked}
    <UnlockScreen onunlocked={refreshAll} />
  {:else}
    <main class="fill"></main>
  {/if}
</div>

<style>
  .window {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  .fill {
    flex: 1;
  }
</style>
