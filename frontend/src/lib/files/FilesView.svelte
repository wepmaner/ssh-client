<script lang="ts">
  import { tick, untrack } from 'svelte'
  import { sideButton } from '../mousenav'
  import { requestClose, sessions, type SessionTab } from '../sessions.svelte'
  import { toast } from '../stores.svelte'
  import ErrorCard from '../terminal/ErrorCard.svelte'
  import { copyText } from '../terminal/keys'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Menu from '../ui/Menu.svelte'
  import FileList from './FileList.svelte'
  import { AUTO_REFRESH_MS, filesTab } from './files.svelte'
  import { plural } from './format'
  import Icon from './Icon.svelte'
  import LoginCard from './LoginCard.svelte'
  import PathBar from './PathBar.svelte'
  import Transfers from './Transfers.svelte'

  /** The files tab of a host: folders of the server over SFTP and the queue of its transfers. */
  let { tab: given, visible, onopenhost }: { tab: SessionTab; visible: boolean; onopenhost: () => void } = $props()

  // A state event replaces the tab object in the list; follow it by id.
  const tab = $derived(sessions.list.find((t) => t.id === given.id) ?? given)
  const model = $derived(filesTab(given.id))

  let pathBar: PathBar | undefined = $state()
  let list: FileList | undefined = $state()
  let filterEl: HTMLInputElement | undefined = $state()
  let uploadBtn: HTMLButtonElement | undefined = $state()
  let editingPath = $state(false)
  let uploadMenu = $state(false)

  /** A short wait before the login card, so a live connection does not flash it. */
  let waited = $state(false)
  $effect(() => {
    if (tab.state !== 'connecting') {
      waited = false
      return
    }
    const t = setTimeout(() => (waited = true), 250)
    return () => clearTimeout(t)
  })

  const showLogin = $derived(!!model && tab.state === 'connecting' && (model.loginSeen || (!model.loaded && waited)))
  const showError = $derived(!!model && !model.loaded && (tab.state === 'error' || tab.state === 'closed'))
  const banner = $derived(!!model && model.loaded && tab.state !== 'connected' && !showLogin)
  const blocked = $derived(showLogin || showError)

  // The list takes the keys when the tab comes to front or the first listing arrives.
  $effect(() => {
    if (visible && model?.loaded && !blocked) void tick().then(() => list?.focus())
  })

  // A folder that failed to open drops the focused row: the list takes the keys back.
  $effect(() => {
    if (!visible || !model?.listError) return
    void tick().then(() => {
      const el = document.activeElement
      if (!el || el === document.body) list?.focus()
    })
  })

  // The listing follows the server on its own: when the tab comes to front, when the window gets the
  // focus back, and every 10 s while it is shown. autoRefresh skips a busy list and stays silent.
  $effect(() => {
    const m = model
    if (!visible || !m || tab.state !== 'connected') return
    const refresh = () => untrack(() => void m.autoRefresh())
    refresh()
    const timer = setInterval(() => document.visibilityState !== 'hidden' && refresh(), AUTO_REFRESH_MS)
    window.addEventListener('focus', refresh)
    return () => {
      clearInterval(timer)
      window.removeEventListener('focus', refresh)
    }
  })

  /** The side buttons of a mouse go back and forward, as in Explorer. */
  function onmouseup(e: MouseEvent): void {
    const dir = sideButton(e)
    if (!dir || !visible || !model || blocked || document.querySelector('[role="dialog"]')) return
    e.preventDefault()
    void (dir === 'back' ? model.back() : model.forward())
  }

  async function copyPaths(paths: string[]): Promise<void> {
    if (paths.length === 0) return
    try {
      await copyText(paths.join('\n'))
      toast(paths.length > 1 ? 'Пути скопированы' : 'Путь скопирован')
    } catch {
      toast('Не удалось скопировать')
    }
  }

  const typing = (e: KeyboardEvent) => (e.target as HTMLElement | null)?.closest?.('input, textarea, [contenteditable]') != null

  function onkeydown(e: KeyboardEvent): void {
    if (!visible || !model) return
    // In WebView2 F5 would reload the whole window, dialog or not.
    if (e.key === 'F5') e.preventDefault()
    if (blocked || document.querySelector('[role="dialog"]')) return
    const ctrl = e.ctrlKey && !e.altKey && !e.metaKey
    if (e.key === 'F5') {
      void model.refresh()
    } else if (ctrl && !e.shiftKey && e.code === 'KeyL') {
      e.preventDefault()
      void pathBar?.edit()
    } else if (ctrl && !e.shiftKey && e.code === 'KeyF') {
      e.preventDefault()
      filterEl?.focus()
      filterEl?.select()
    } else if (e.altKey && !e.ctrlKey && e.key === 'ArrowLeft') {
      e.preventDefault()
      void model.back()
    } else if (e.altKey && !e.ctrlKey && e.key === 'ArrowRight') {
      e.preventDefault()
      void model.forward()
    } else if (e.altKey && !e.ctrlKey && e.key === 'ArrowUp') {
      e.preventDefault()
      void model.up()
    } else if (e.key === 'Backspace' && !e.ctrlKey && !e.altKey && !e.defaultPrevented && !typing(e)) {
      // The list handles it while it has the focus; this is for a folder that failed to open.
      e.preventDefault()
      void model.up()
    } else if (ctrl && e.shiftKey && e.code === 'KeyN' && !typing(e)) {
      e.preventDefault()
      model.startCreate()
    }
  }

  function onfilterkey(e: KeyboardEvent): void {
    if (!model) return
    if (e.key === 'Escape' && model.filter) {
      e.preventDefault()
      e.stopPropagation()
      model.filter = ''
    } else if (e.key === 'ArrowDown' || e.key === 'Enter') {
      e.preventDefault()
      list?.focus()
    }
  }

  const conflictText = $derived.by(() => {
    const names = model?.conflict?.names ?? []
    const shown = names.slice(0, 3).join(', ')
    return names.length > 3 ? `${shown} (и ещё ${names.length - 3})` : shown
  })

  const deleteHasDir = $derived.by(() => {
    const n = new Set(model?.deleteAsk ?? [])
    return (model?.entries ?? []).some((e) => e.dir && n.has(e.name))
  })
  const deleteTitle = $derived.by(() => {
    const n = model?.deleteAsk ?? []
    if (n.length === 1) return `Удалить «${n[0]}»?`
    return `Удалить ${n.length} ${plural(n.length, 'объект', 'объекта', 'объектов')}?`
  })
</script>

<svelte:window {onkeydown} {onmouseup} />

{#if model}
  <div class="view" aria-hidden={blocked ? 'true' : undefined}>
    <div class="nav">
      <button type="button" class="icon" aria-label="Назад" title="Назад (Alt+←, боковая кнопка мыши)" disabled={!model.canBack} onclick={() => model.back()}>
        <Icon name="back" />
      </button>
      <button type="button" class="icon" aria-label="Вперёд" title="Вперёд (Alt+→, боковая кнопка мыши)" disabled={!model.canForward} onclick={() => model.forward()}>
        <Icon name="forward" />
      </button>
      <button type="button" class="icon" aria-label="Вверх" title="На уровень вверх (Alt+↑)" disabled={!model.canUp} onclick={() => model.up()}>
        <Icon name="up" />
      </button>
      <button type="button" class="icon" aria-label="Обновить" title="Обновить (F5)" disabled={!model.path} onclick={() => model.refresh()}>
        <Icon name="refresh" />
      </button>
      <PathBar bind:this={pathBar} bind:editing={editingPath} {model} ondone={() => void tick().then(() => list?.focus())} />
      <label class="filter" title="Поиск в папке (Ctrl+F)">
        <Icon name="search" size={14} />
        <input
          bind:this={filterEl}
          bind:value={model.filter}
          type="text"
          placeholder="Поиск в папке"
          aria-label="Поиск по имени в текущей папке"
          autocomplete="off"
          spellcheck="false"
          onkeydown={onfilterkey}
        />
      </label>
    </div>

    <div class="commands">
      <button type="button" class="cmd" aria-haspopup="menu" aria-expanded={uploadMenu} bind:this={uploadBtn} onclick={() => (uploadMenu = !uploadMenu)}>
        <Icon name="upload" /><span>Загрузить</span><Icon name="chevron" size={12} />
      </button>
      {#if uploadMenu}
        <Menu
          anchor={uploadBtn}
          at={uploadBtn ? { x: uploadBtn.getBoundingClientRect().left, y: uploadBtn.getBoundingClientRect().bottom + 4 } : undefined}
          onclose={() => (uploadMenu = false)}
          items={[
            { label: 'Файлы…', onselect: () => void model.upload(false) },
            { label: 'Папку…', onselect: () => void model.upload(true) },
          ]}
        />
      {/if}
      <button type="button" class="cmd" title="Новая папка (Ctrl+Shift+N)" disabled={!model.path || !!model.listError} onclick={() => model.startCreate()}>
        <Icon name="newfolder" /><span>Новая папка</span>
      </button>
      <button type="button" class="cmd" disabled={model.selectedEntries.length === 0} onclick={() => model.download()}>
        <Icon name="download" /><span>Скачать…</span>
      </button>
      <span class="spacer"></span>
      <button
        type="button"
        class="cmd toggle"
        aria-pressed={model.showHidden}
        title={model.showHidden ? 'Скрытые показаны: имена, начинающиеся с точки' : 'Показать имена, начинающиеся с точки'}
        onclick={() => model.toggleHidden()}
      >
        <Icon name="eye" /><span>Скрытые</span>
      </button>
    </div>

    {#if banner}
      <div class="banner" role="status">
        <Icon name="warning" size={14} />
        {#if tab.state === 'connecting'}
          <span>Связь с сервером прервана. Переподключение…</span>
        {:else}
          <span>Связь с сервером прервана{tab.message ? `: ${tab.message}` : '.'}</span>
          <span class="spacer"></span>
          <Button variant="outline" onclick={() => model.retry()} style="height:26px;padding:0 10px;font-size:12px">Повторить</Button>
        {/if}
      </div>
    {/if}

    <FileList bind:this={list} {model} oncopypath={copyPaths} />

    {#if model.jobs.length > 0}<Transfers {model} />{/if}
  </div>

  {#if showLogin}
    <LoginCard {model} hostName={tab.hostName} onclose={() => requestClose(tab.id)} />
  {:else if showError}
    <ErrorCard
      hostName={tab.hostName}
      message={tab.message || (tab.state === 'closed' ? 'Подключение закрыто.' : 'Не удалось подключиться.')}
      detail={tab.detail}
      retryable={tab.retryable || tab.state === 'closed'}
      onretry={() => model.retry()}
      {onopenhost}
    />
  {/if}

  <Dialog open={!!model.conflict} title="Такие имена уже есть" onclose={() => model.conflict?.resolve(null)} width={460} autofocus="button.btn.primary">
    <p class="muted">В папке уже есть: <span class="names">{conflictText}</span></p>
    {#if model.conflict?.merge}
      <p class="muted small">Папки с тем же именем сливаются, файлы внутри них следуют тому же выбору.</p>
    {/if}
    {#snippet footer()}
      <span></span>
      <span class="actions">
        <Button variant="ghost" onclick={() => model.conflict?.resolve(null)}>Отмена</Button>
        <Button variant="outline" onclick={() => model.conflict?.resolve('skip')}>Пропустить</Button>
        <Button variant="primary" onclick={() => model.conflict?.resolve('replace')}>Заменить</Button>
      </span>
    {/snippet}
  </Dialog>

  <!-- Cancel has the focus: Enter never deletes by itself. -->
  <Dialog open={!!model.deleteAsk} title={deleteTitle} onclose={() => (model.deleteAsk = null)} width={440} autofocus="[data-autofocus]">
    <p class="muted">{deleteHasDir ? 'Папки удаляются со всем содержимым. ' : ''}Это нельзя отменить.</p>
    {#snippet footer()}
      <span></span>
      <span class="actions">
        <Button variant="outline" data-autofocus="" onclick={() => (model.deleteAsk = null)}>Отмена</Button>
        <Button variant="danger" onclick={() => model.confirmDelete()}>Удалить</Button>
      </span>
    {/snippet}
  </Dialog>
{/if}

<style>
  .view {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: var(--bg);
  }
  .nav {
    display: flex;
    flex: none;
    align-items: center;
    gap: 4px;
    padding: 8px 12px 4px;
  }
  .icon {
    display: grid;
    flex: none;
    place-items: center;
    width: 32px;
    height: 32px;
    padding: 0;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    cursor: default;
    transition: background-color 80ms;
  }
  .icon:hover:not(:disabled) {
    background: #262b33;
  }
  .icon:disabled {
    color: #5f6873;
  }
  .icon:focus-visible,
  .cmd:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .icon:last-of-type {
    margin-right: 4px;
  }
  .filter {
    display: flex;
    flex: 0 1 240px;
    align-items: center;
    gap: 8px;
    min-width: 140px;
    height: var(--control-height);
    margin-left: 4px;
    padding: 0 10px;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
    color: var(--muted);
  }
  .filter:focus-within {
    border-color: var(--accent);
  }
  .filter input {
    flex: 1;
    min-width: 0;
    border: 0;
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 13px;
    outline: none;
    user-select: text;
  }
  .commands {
    display: flex;
    flex: none;
    align-items: center;
    gap: 2px;
    padding: 4px 12px 6px;
    border-bottom: 1px solid var(--border);
  }
  .cmd {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 30px;
    padding: 0 10px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 13px;
    white-space: nowrap;
    cursor: default;
    transition: background-color 80ms;
  }
  .cmd:hover:not(:disabled),
  .cmd[aria-expanded='true'] {
    background: #262b33;
  }
  /* A pressed toggle reads as on: accent tint and accent text, not just a hover shade. */
  .cmd.toggle[aria-pressed='true'] {
    background: rgba(79, 143, 247, 0.16);
    color: var(--accent);
  }
  .cmd.toggle[aria-pressed='true']:hover {
    background: rgba(79, 143, 247, 0.24);
  }
  .cmd:disabled {
    color: #5f6873;
  }
  .spacer {
    flex: 1;
  }
  .banner {
    display: flex;
    flex: none;
    align-items: center;
    gap: 8px;
    min-height: 34px;
    padding: 4px 16px;
    background: rgba(227, 179, 65, 0.1);
    border-bottom: 1px solid rgba(227, 179, 65, 0.25);
    color: #e3c47a;
    font-size: 12.5px;
  }
  .muted {
    margin: 0;
    color: var(--muted);
    line-height: 1.5;
  }
  .small {
    margin-top: 8px;
    font-size: 12px;
  }
  .names {
    color: var(--text);
    word-break: break-word;
    user-select: text;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
