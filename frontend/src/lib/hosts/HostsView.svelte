<script lang="ts">
  import { untrack } from 'svelte'
  import { GroupDelete, GroupRename, GroupSave, HostDelete, errorText, type app } from '../api'
  import { closeSession, openSession, sessions } from '../sessions.svelte'
  import { groups, hosts, refreshHosts, refreshKeys, toast, ui } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'
  import EmptyState from './EmptyState.svelte'
  import { fieldError } from './format'
  import GroupTile from './GroupTile.svelte'
  import HostDialog from './HostDialog.svelte'
  import HostTile from './HostTile.svelte'

  type Layout = 'tile' | 'row'
  const LAYOUT_KEY = 'hosts.layout'

  function loadLayout(): Layout {
    try {
      return localStorage.getItem(LAYOUT_KEY) === 'row' ? 'row' : 'tile'
    } catch {
      return 'tile'
    }
  }

  let layout = $state<Layout>(loadLayout())
  let query = $state('')
  let groupFilter = $state('')
  let searchEl: HTMLInputElement | undefined = $state()
  let dialog = $state<{ open: boolean; host: app.HostDTO | null }>({ open: false, host: null })
  let confirm = $state<{ title: string; text: string; run: () => Promise<void> } | null>(null)
  let groupEdit = $state<{ id: string; name: string; error: string } | null>(null)

  const filtered = $derived.by(() => {
    const q = query.trim().toLowerCase()
    return hosts.filter(
      (h) =>
        (!groupFilter || h.group_id === groupFilter) &&
        (!q || [h.name, h.address, h.user].some((s) => s.toLowerCase().includes(q))),
    )
  })
  const connected = $derived(hosts.filter((h) => h.connected).length)

  // Connected marks come from Go: reload whenever a session changes state.
  const sessionStates = $derived(sessions.list.map((t) => t.hostId + ':' + t.state).join())
  $effect(() => {
    void sessionStates
    untrack(() => void refreshHosts().catch(() => {}))
  })

  // An error card asks to open its host.
  $effect(() => {
    const id = ui.editHostId
    if (!id) return
    untrack(() => {
      ui.editHostId = ''
      const h = hosts.find((x) => x.id === id)
      if (h) openDialog(h)
    })
  })

  function setLayout(l: Layout) {
    layout = l
    try {
      localStorage.setItem(LAYOUT_KEY, l)
    } catch {
      // The choice simply is not remembered.
    }
  }

  function onkeydown(e: KeyboardEvent) {
    if (ui.activeTab !== 'home' || dialog.open || confirm || groupEdit) return
    if (!e.ctrlKey || e.shiftKey || e.altKey) return
    if (e.code !== 'KeyK' && e.key.toLowerCase() !== 'k') return
    e.preventDefault()
    searchEl?.focus()
    searchEl?.select()
  }

  function openDialog(host: app.HostDTO | null) {
    dialog = { open: true, host }
  }

  function duplicate(h: app.HostDTO) {
    openDialog({ ...h, id: '', name: `${h.name} (копия)`, connected: false })
  }

  function askDeleteHost(h: app.HostDTO) {
    confirm = {
      title: `Удалить хост ${h.name}?`,
      text: 'Хост будет удалён из хранилища, его открытые вкладки закроются. Ключ останется в разделе «Ключи».',
      run: async () => {
        await HostDelete(h.id)
        for (const t of sessions.list.filter((t) => t.hostId === h.id)) closeSession(t.id)
        await Promise.all([refreshHosts(), refreshKeys()])
      },
    }
  }

  function askDeleteGroup(g: app.GroupDTO) {
    confirm = {
      title: `Удалить группу ${g.name}?`,
      text: 'Хосты группы останутся, без группы.',
      run: async () => {
        await GroupDelete(g.id)
        if (groupFilter === g.id) groupFilter = ''
        await refreshHosts()
      },
    }
  }

  async function runConfirm() {
    const c = confirm
    confirm = null
    try {
      await c?.run()
    } catch (e) {
      toast(errorText(e))
    }
  }

  async function saveGroup() {
    const g = groupEdit
    if (!g) return
    try {
      if (g.id) await GroupRename(g.id, g.name)
      else await GroupSave(g.name)
      groupEdit = null
      await refreshHosts()
    } catch (e) {
      const msg = errorText(e)
      g.error = fieldError(msg)?.text ?? msg
    }
  }
</script>

<svelte:window {onkeydown} />

{#snippet plus()}
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
    <path d="M8 3v10M3 8h10" />
  </svg>
{/snippet}

<div class="view">
  <div class="toolbar">
    <Button variant="primary" onclick={() => openDialog(null)}>
      <span class="with-icon">{@render plus()}Новый хост</span>
    </Button>
    <label class="search">
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" aria-hidden="true">
        <circle cx="7" cy="7" r="4.5" /><path d="m10.5 10.5 3.5 3.5" />
      </svg>
      <input
        bind:this={searchEl}
        bind:value={query}
        type="text"
        placeholder="Поиск по имени или адресу"
        aria-label="Поиск хостов"
        autocomplete="off"
        spellcheck="false"
        disabled={hosts.length === 0}
      />
      <kbd>Ctrl+K</kbd>
    </label>
    <span class="spacer"></span>
    {#if hosts.length > 0}
      <div class="layout" role="group" aria-label="Вид">
        <button type="button" aria-pressed={layout === 'tile'} aria-label="Плитки" title="Плитки" onclick={() => setLayout('tile')}>
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <rect x="2" y="2" width="5" height="5" rx="1" /><rect x="9" y="2" width="5" height="5" rx="1" />
            <rect x="2" y="9" width="5" height="5" rx="1" /><rect x="9" y="9" width="5" height="5" rx="1" />
          </svg>
        </button>
        <button type="button" aria-pressed={layout === 'row'} aria-label="Список" title="Список" onclick={() => setLayout('row')}>
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true">
            <path d="M2 4h12M2 8h12M2 12h12" />
          </svg>
        </button>
      </div>
    {/if}
  </div>

  {#if hosts.length === 0}
    <EmptyState onadd={() => openDialog(null)} oncreatekey={() => (ui.view = 'keys')} />
  {:else}
    <section>
      <h2>Группы</h2>
      <div class="groups" role="list">
        {#each groups as g (g.id)}
          <GroupTile
            group={g}
            selected={groupFilter === g.id}
            onselect={() => (groupFilter = groupFilter === g.id ? '' : g.id)}
            onrename={() => (groupEdit = { id: g.id, name: g.name, error: '' })}
            ondelete={() => askDeleteGroup(g)}
          />
        {/each}
        <button type="button" class="add group-add" onclick={() => (groupEdit = { id: '', name: '', error: '' })}>
          {@render plus()}Новая группа
        </button>
      </div>
    </section>

    <section>
      <div class="heading">
        <h2>Хосты</h2>
        <span class="count">{hosts.length}{connected > 0 ? ` · подключены ${connected}` : ''}</span>
      </div>
      <div class="hosts {layout}" role="list">
        {#each filtered as h (h.id)}
          <HostTile
            host={h}
            {layout}
            onconnect={() => openSession(h.id)}
            onedit={() => openDialog(h)}
            onduplicate={() => duplicate(h)}
            ondelete={() => askDeleteHost(h)}
          />
        {:else}
          <p class="none">Ничего не найдено</p>
        {/each}
        {#if layout === 'tile' && !query.trim() && !groupFilter}
          <button type="button" class="add host-add" onclick={() => openDialog(null)}>{@render plus()}Добавить хост</button>
        {/if}
      </div>
    </section>
  {/if}
</div>

<HostDialog open={dialog.open} host={dialog.host} onclose={() => (dialog = { open: false, host: null })} />

<Dialog open={confirm !== null} title={confirm?.title ?? ''} onclose={() => (confirm = null)} width={420}>
  <p class="muted">{confirm?.text}</p>
  {#snippet footer()}
    <span></span>
    <span class="actions">
      <Button variant="ghost" onclick={() => (confirm = null)}>Отмена</Button>
      <Button variant="primary" onclick={runConfirm}>Удалить</Button>
    </span>
  {/snippet}
</Dialog>

<Dialog open={groupEdit !== null} title={groupEdit?.id ? 'Переименовать группу' : 'Новая группа'} onclose={() => (groupEdit = null)} width={400}>
  {#if groupEdit}
    <form
      id="group-form"
      onsubmit={(e) => {
        e.preventDefault()
        void saveGroup()
      }}
    >
      <label class="field">
        <span>Название группы</span>
        <Input bind:value={groupEdit.name} autocomplete="off" oninput={() => groupEdit && (groupEdit.error = '')} />
      </label>
      {#if groupEdit.error}<p class="error" role="alert">{groupEdit.error}</p>{/if}
    </form>
  {/if}
  {#snippet footer()}
    <span></span>
    <span class="actions">
      <Button variant="ghost" onclick={() => (groupEdit = null)}>Отмена</Button>
      <Button variant="primary" type="submit" form="group-form">{groupEdit?.id ? 'Переименовать' : 'Создать'}</Button>
    </span>
  {/snippet}
</Dialog>

<style>
  .view {
    display: flex;
    flex-direction: column;
    gap: 22px;
    height: 100%;
    padding: 14px 24px 32px;
    overflow: auto;
  }
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px;
  }
  .with-icon {
    display: inline-flex;
    align-items: center;
    gap: 8px;
  }
  .spacer {
    flex: 1;
  }
  .search {
    display: flex;
    flex: 0 1 340px;
    align-items: center;
    gap: 8px;
    height: var(--control-height);
    padding: 0 10px;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
    color: var(--muted);
  }
  .search:focus-within {
    border-color: var(--accent);
  }
  .search input {
    flex: 1;
    min-width: 0;
    border: 0;
    background: transparent;
    color: var(--text);
    font: inherit;
    outline: none;
    user-select: text;
  }
  kbd {
    padding: 1px 5px;
    border: 1px solid #333a44;
    border-radius: 3px;
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .layout {
    display: flex;
    gap: 2px;
    height: var(--control-height);
    padding: 2px;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
  }
  .layout button {
    display: grid;
    place-items: center;
    width: 30px;
    border: 0;
    border-radius: 3px;
    background: transparent;
    color: var(--muted);
    cursor: default;
    transition: background-color 80ms;
  }
  .layout button:hover {
    color: var(--text);
  }
  .layout button[aria-pressed='true'] {
    background: #2e333b;
    color: var(--text);
  }
  .layout button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
  section {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .heading {
    display: flex;
    align-items: baseline;
    gap: 8px;
  }
  h2 {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0.02em;
  }
  .count {
    color: var(--muted);
    font-size: 11px;
  }
  .groups {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(240px, 100%), 1fr));
    gap: 10px;
  }
  .hosts.tile {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(300px, 100%), 1fr));
    gap: 10px;
  }
  .hosts.row {
    display: flex;
    flex-direction: column;
    overflow: hidden;
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
  }
  .add {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    border: 1px dashed #333a44;
    border-radius: var(--radius-card);
    background: transparent;
    color: var(--muted);
    font: inherit;
    cursor: default;
    transition: background-color 80ms;
  }
  .add:hover {
    background: #22262d;
    color: var(--text);
  }
  .add:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  .group-add {
    height: 56px;
  }
  .host-add {
    height: 64px;
  }
  .none {
    margin: 0;
    padding: 12px 0;
    color: var(--muted);
  }
  .hosts.row .none {
    padding: 12px 14px;
  }
  .muted {
    margin: 0;
    color: var(--muted);
    line-height: 1.45;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
  .field span {
    display: block;
    margin-bottom: 6px;
    color: var(--muted);
    font-size: 12px;
  }
  .error {
    margin: 10px 0 0;
    color: var(--error);
    font-size: 12px;
  }
  @media (prefers-reduced-motion: reduce) {
    .layout button,
    .add {
      transition: none;
    }
  }
</style>
