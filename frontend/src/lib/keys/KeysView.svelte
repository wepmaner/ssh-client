<script lang="ts">
  import { VaultRemovePassword, type app } from '../api'
  import VaultDialog from '../settings/VaultDialog.svelte'
  import { keys, refreshKeys, refreshVault, vault } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'
  import PasswordDialog from '../ui/PasswordDialog.svelte'
  import { hostsLabel, typeLabel, typeTile } from './format'
  import KeyCreateDialog from './KeyCreateDialog.svelte'
  import KeyDetail from './KeyDetail.svelte'
  import KeyImportPanel from './KeyImportPanel.svelte'
  import Menu from '../ui/Menu.svelte'

  let query = $state('')
  let selectedId = $state('')
  let creating = $state(false)
  let importing = $state(false)
  let encrypting = $state(false)
  let decrypting = $state(false)
  let detail: ReturnType<typeof KeyDetail> | undefined = $state()
  let menuAt = $state<{ x: number; y: number } | null>(null)

  function openMenu(e: MouseEvent, id: string) {
    e.preventDefault()
    selectedId = id
    menuAt = { x: e.clientX, y: e.clientY }
  }

  const filtered = $derived.by(() => {
    const q = query.trim().toLowerCase()
    if (!q) return keys
    return keys.filter((k) => [k.name, k.comment, k.fingerprint].some((s) => s.toLowerCase().includes(q)))
  })
  const selected = $derived(filtered.find((k) => k.id === selectedId) ?? filtered[0])

  async function onNewKey(key: app.KeyDTO) {
    await refreshKeys()
    query = ''
    selectedId = key.id
  }

  async function imported(key: app.KeyDTO) {
    importing = false
    await onNewKey(key)
  }

  async function disableEncryption(password: string) {
    await VaultRemovePassword(password)
    await refreshVault()
  }
</script>

{#snippet actions()}
  <Button variant="primary" onclick={() => (creating = true)}>Создать ключ</Button>
  <Button variant="outline" onclick={() => (importing = true)}>
    <span class="with-icon">
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M8 11V2.5M4.5 6 8 2.5 11.5 6M2.5 11v2.5h11V11" />
      </svg>
      Импортировать
    </span>
  </Button>
{/snippet}

<div class="view">
  <div class="toolbar">
    {@render actions()}
    <span class="muted">OpenSSH, PEM, PuTTY .ppk</span>
    <span class="spacer"></span>
    <div class="search"><Input bind:value={query} placeholder="Поиск ключа" aria-label="Поиск ключа" autocomplete="off" /></div>
  </div>

  <div class="status">
    <span class="tile" class:ok={vault.has_password} aria-hidden="true">
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
        <rect x="3.5" y="7" width="9" height="6.5" rx="1" />
        {#if vault.has_password}<path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />{:else}<path d="M5.5 7V5a2.5 2.5 0 0 1 4.7-1.2" />{/if}
      </svg>
    </span>
    <div class="status-text">
      {#if vault.has_password}
        <strong>Хранилище зашифровано паролем</strong>
        <span class="muted">Пароль спрашивается при запуске. Забытый пароль восстановить нельзя.</span>
      {:else}
        <strong>Хранилище не зашифровано паролем</strong>
        <span class="muted">Ключи защищены учётной записью Windows. Пароль добавит защиту, если к компьютеру есть доступ у других.</span>
      {/if}
    </div>
    {#if vault.has_password}
      <Button variant="outline" onclick={() => (decrypting = true)}>Отключить шифрование</Button>
    {:else}
      <Button variant="outline" onclick={() => (encrypting = true)}>Включить шифрование</Button>
    {/if}
  </div>

  {#if keys.length === 0}
    <div class="empty">
      <p class="muted">Ключей пока нет</p>
      <div class="empty-actions">{@render actions()}</div>
    </div>
  {:else}
    <div class="body">
      <ul class="list" aria-label="Ключи">
        {#each filtered as k (k.id)}
          <li>
            <button type="button" class="row" class:sel={k.id === selected?.id} aria-current={k.id === selected?.id} onclick={() => (selectedId = k.id)} oncontextmenu={(e) => openMenu(e, k.id)}>
              <span class="type">{typeTile(k.type)}</span>
              <span class="info">
                <span class="name">{k.name}</span>
                <span class="muted sub">{typeLabel(k.type)} · {hostsLabel(k.host_ids.length)}</span>
              </span>
            </button>
          </li>
        {:else}
          <li class="none muted">Ничего не найдено</li>
        {/each}
      </ul>
      {#if selected}
        <KeyDetail bind:this={detail} key={selected} onchanged={refreshKeys} />
      {/if}
      {#if menuAt}
        <Menu
          at={menuAt}
          onclose={() => (menuAt = null)}
          items={[
            { label: 'Переименовать', onselect: () => detail?.requestRename() },
            { label: 'Удалить', onselect: () => detail?.requestDelete(), danger: true },
          ]}
        />
      {/if}
    </div>
  {/if}
</div>

<KeyCreateDialog open={creating} onclose={() => (creating = false)} oncreated={onNewKey} />

<Dialog open={importing} title="Импорт ключа" onclose={() => (importing = false)} width={480}>
  <KeyImportPanel onimported={imported} />
</Dialog>

<VaultDialog open={encrypting} onclose={() => (encrypting = false)} onenabled={refreshVault} />

<PasswordDialog
  open={decrypting}
  title="Отключить шифрование"
  hint="Хранилище снова будет защищено только учётной записью Windows. Введите текущий пароль."
  confirmLabel="Отключить"
  onsubmit={disableEncryption}
  onclose={() => (decrypting = false)}
/>

<style>
  .view {
    display: flex;
    flex-direction: column;
    gap: 14px;
    height: 100%;
    padding: 16px 20px;
  }
  .toolbar {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .spacer {
    flex: 1;
  }
  .search {
    width: 260px;
  }
  .muted {
    color: var(--muted);
  }
  .with-icon {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .status {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px;
    padding: 10px 14px;
    background: #22262d;
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
  }
  .tile {
    display: grid;
    place-items: center;
    flex: none;
    width: 32px;
    height: 32px;
    border-radius: var(--radius-control);
    background: #3a3320;
    color: #e3b341;
  }
  .tile.ok {
    background: #1f3a2a;
    color: var(--ok);
  }
  .status-text {
    display: flex;
    flex: 1 1 240px;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
    font-size: 13px;
  }
  .status-text .muted {
    font-size: 12px;
  }
  .empty {
    display: flex;
    flex: 1;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 14px;
  }
  .empty p {
    margin: 0;
  }
  .empty-actions {
    display: flex;
    gap: 10px;
  }
  .body {
    display: flex;
    flex: 1;
    gap: 14px;
    min-height: 0;
  }
  .list {
    flex: 0 0 340px;
    margin: 0;
    padding: 0;
    overflow: auto;
    list-style: none;
    background: #1e2228;
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    height: 56px;
    padding: 0 14px;
    border: 0;
    background: transparent;
    color: var(--text);
    font: inherit;
    text-align: left;
    cursor: default;
    transition: background-color 80ms;
  }
  .row:hover {
    background: #22262d;
  }
  .row.sel {
    background: #262b33;
    box-shadow: inset 2px 0 0 var(--accent);
  }
  .row:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .type {
    display: grid;
    place-items: center;
    flex: none;
    width: 34px;
    height: 34px;
    background: #2e333b;
    border-radius: var(--radius-control);
    font-family: var(--font-mono);
    font-size: 12px;
  }
  .info {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sub {
    font-size: 12px;
  }
  .none {
    padding: 16px;
  }
</style>
