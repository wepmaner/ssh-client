<script lang="ts">
  import { onMount } from 'svelte'
  import HostsView from './lib/hosts/HostsView.svelte'
  import KeysView from './lib/keys/KeysView.svelte'
  import { goHome, retrySession, sessions } from './lib/sessions.svelte'
  import CorruptScreen from './lib/settings/CorruptScreen.svelte'
  import UnlockScreen from './lib/settings/UnlockScreen.svelte'
  import Sidebar from './lib/sidebar/Sidebar.svelte'
  import StatusBar from './lib/StatusBar.svelte'
  import { refreshAll, ui, vault, type View } from './lib/stores.svelte'
  import ErrorCard from './lib/terminal/ErrorCard.svelte'
  import TerminalView from './lib/terminal/TerminalView.svelte'
  import TitleBar from './lib/titlebar/TitleBar.svelte'
  import Toast from './lib/ui/Toast.svelte'

  const titles: Record<View, string> = {
    hosts: 'Хосты',
    keys: 'Ключи',
    forwards: 'Пробросы',
    settings: 'Настройки',
  }
  const shell = $derived(vault.ready && !vault.corrupt && !vault.locked)
  const home = $derived(ui.activeTab === 'home' || !sessions.list.some((t) => t.id === sessions.activeId))

  function openHost(hostId: string): void {
    ui.view = 'hosts'
    ui.editHostId = hostId
    goHome()
  }

  onMount(refreshAll)
</script>

<div class="window">
  <TitleBar title={shell ? titles[ui.view] : ''} tabs={shell} />
  {#if !vault.ready}
    <div class="fill"></div>
  {:else if vault.corrupt}
    <CorruptScreen error={vault.error} onreset={refreshAll} />
  {:else if vault.locked}
    <UnlockScreen onunlocked={refreshAll} />
  {:else}
    <div class="shell" class:hidden={!home}>
      <Sidebar />
      <main class="fill">
        {#if ui.view === 'keys'}
          <KeysView />
        {:else if ui.view === 'hosts'}
          <HostsView />
        {:else}
          <h1 class="placeholder">{titles[ui.view]}</h1>
        {/if}
      </main>
    </div>
    <!-- Every terminal stays mounted so its scrollback survives tab switches. -->
    <div class="terminals" class:hidden={home}>
      {#each sessions.list as tab (tab.id)}
        {@const visible = !home && tab.id === sessions.activeId}
        <div class="pane" class:hidden={!visible}>
          <TerminalView sessionId={tab.id} {visible} />
          {#if tab.state === 'error'}
            <ErrorCard
              hostName={tab.hostName}
              kind={tab.kind}
              message={tab.message}
              detail={tab.detail}
              retryable={tab.retryable}
              onretry={() => retrySession(tab.id)}
              onopenhost={() => openHost(tab.hostId)}
            />
          {/if}
        </div>
      {/each}
    </div>
    <StatusBar />
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
  .terminals {
    position: relative;
    flex: 1;
    min-height: 0;
    background: var(--terminal);
  }
  .pane {
    position: absolute;
    inset: 0;
  }
  .hidden {
    display: none;
  }
  .fill {
    flex: 1;
    min-width: 0;
  }
  h1.placeholder {
    margin: 24px;
    font-size: 16px;
    font-weight: 600;
  }
</style>
