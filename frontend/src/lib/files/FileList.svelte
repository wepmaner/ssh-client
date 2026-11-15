<script lang="ts">
  import { tick } from 'svelte'
  import Menu, { type MenuItem } from '../ui/Menu.svelte'
  import { filesClipboard, MOVE_TYPE, type FilesTab } from './files.svelte'
  import { formatDate, formatSize, joinPath, objectsLabel, stemRange, type Entry, type SortKey } from './format'
  import { watchDragOut } from './dragout'
  import Icon from './Icon.svelte'
  import { clickSelect, keySelect, selectAll, type NavKey } from './selection'

  interface Props {
    model: FilesTab
    /** Copies the paths of the selection, or of the folder when nothing is selected. */
    oncopypath: (paths: string[]) => void
  }
  let { model, oncopypath }: Props = $props()

  let listEl: HTMLDivElement | undefined = $state()
  // The last menu stays as it was after closing: Menu still reads its anchor while it closes.
  let menuOpen = $state(false)
  let lastMenu = $state<{ items: MenuItem[]; at?: { x: number; y: number }; anchor?: HTMLElement } | null>(null)
  const menu = $derived(menuOpen ? lastMenu : null)

  function showMenu(m: { items: MenuItem[]; at?: { x: number; y: number }; anchor?: HTMLElement }): void {
    lastMenu = m
    menuOpen = true
  }
  /** Folder row under an internal drag. */
  let over = $state('')

  const columns: { key: SortKey; label: string; cls: string }[] = [
    { key: 'name', label: 'Имя', cls: 'name' },
    { key: 'mtime', label: 'Изменён', cls: 'date' },
    { key: 'size', label: 'Размер', cls: 'size' },
    { key: 'mode', label: 'Права', cls: 'mode' },
  ]

  const footer = $derived.by(() => {
    if (model.listError) return ''
    const parts = [objectsLabel(model.visible.length)]
    const sel = model.selectedEntries
    if (sel.length > 0) {
      parts.push(`выбрано ${sel.length}`)
      const files = sel.filter((e) => !e.dir)
      if (files.length > 0) parts.push(formatSize(files.reduce((s, e) => s + e.size, 0)))
    }
    return parts.join(' · ')
  })

  /** Paths cut to the clipboard from this server: their rows are dimmed, as in Explorer. */
  const cutPaths = $derived.by(() => {
    const c = filesClipboard.value
    return c && c.cut && c.hostId === model.hostId ? new Set(c.paths) : new Set<string>()
  })

  const rowId = (name: string) => `f-${model.id}-${encodeURIComponent(name)}`
  const isSel = (name: string) => model.sel.selected.includes(name)
  const isFolder = (e: Entry) => e.dir && !e.broken

  /** Focus on the list: the cursor row, or the list itself when it has none. */
  export function focus(options?: FocusOptions): void {
    const row = model.sel.cursor ? document.getElementById(rowId(model.sel.cursor)) : null
    if (row) row.focus(options)
    else listEl?.focus(options)
  }

  async function focusCursor(): Promise<void> {
    await tick()
    const row = document.getElementById(rowId(model.sel.cursor))
    row?.focus()
    row?.scrollIntoView?.({ block: 'nearest' })
  }

  function onrowclick(e: MouseEvent, entry: Entry): void {
    if (model.renaming === entry.name) return
    model.sel = clickSelect(model.sel, model.order, entry.name, { ctrl: e.ctrlKey, shift: e.shiftKey })
    // A draggable row does not take the focus on its own: the keys that follow go to the row, not the list.
    const row = e.currentTarget as HTMLElement
    row.focus({ preventScroll: true })
  }

  /** Space selects the row, Ctrl+Space toggles it, as in Explorer. */
  function onrowkey(e: KeyboardEvent, entry: Entry): void {
    if (e.key !== ' ' || e.altKey || (e.target as HTMLElement).closest('input')) return
    e.preventDefault()
    model.sel = clickSelect(model.sel, model.order, entry.name, { ctrl: e.ctrlKey, shift: e.shiftKey })
  }

  function onblankclick(e: MouseEvent): void {
    if ((e.target as HTMLElement).closest('[role="option"], [role="alert"]')) return
    if (!e.ctrlKey && !e.shiftKey) model.sel = { ...model.sel, selected: [] }
    focus({ preventScroll: true })
  }

  // ---- context menus ----

  function itemMenu(entry: Entry): MenuItem[] {
    const names = model.sel.selected
    const one = names.length === 1
    const items: MenuItem[] = []
    if (one && isFolder(entry)) items.push({ label: 'Открыть', hint: 'Enter', onselect: () => void model.open(entry) })
    if (!entry.broken || names.length > 1) items.push({ label: 'Скачать…', hint: one && !isFolder(entry) ? 'Enter' : undefined, onselect: () => void model.download(names) })
    items.push({ label: 'Вырезать', hint: 'Ctrl+X', onselect: () => model.toClipboard(names, true) })
    items.push({ label: 'Копировать', hint: 'Ctrl+C', onselect: () => model.toClipboard(names, false) })
    if (one && isFolder(entry) && model.canPaste) items.push({ label: 'Вставить в папку', onselect: () => void model.paste(model.pathOf(entry.name)) })
    if (one) items.push({ label: 'Переименовать', hint: 'F2', onselect: () => model.startRename(entry.name) })
    items.push({ label: names.length > 1 ? 'Копировать пути' : 'Копировать путь', hint: 'Ctrl+Shift+C', onselect: () => oncopypath(names.map((n) => model.pathOf(n))) })
    items.push({ label: 'Удалить', hint: 'Del', onselect: () => model.askDelete(names), danger: true })
    return items
  }

  function blankMenu(): MenuItem[] {
    const items: MenuItem[] = []
    if (model.canPaste) items.push({ label: 'Вставить', hint: 'Ctrl+V', onselect: () => void model.paste() })
    items.push(
      { label: 'Загрузить файлы…', onselect: () => void model.upload(false) },
      { label: 'Загрузить папку…', onselect: () => void model.upload(true) },
      { label: 'Новая папка', hint: 'Ctrl+Shift+N', onselect: () => model.startCreate() },
      { label: 'Выделить всё', hint: 'Ctrl+A', onselect: () => (model.sel = selectAll(model.order)) },
      { label: 'Обновить', hint: 'F5', onselect: () => void model.refresh() },
      { label: 'Копировать путь папки', hint: 'Ctrl+Shift+C', onselect: () => oncopypath([model.path]) },
      { label: model.showHidden ? 'Скрыть скрытые' : 'Показать скрытые', onselect: () => model.toggleHidden() },
    )
    return items
  }

  const point = (e: MouseEvent) => (e.clientX === 0 && e.clientY === 0 ? undefined : { x: e.clientX, y: e.clientY })

  function onrowmenu(e: MouseEvent, entry: Entry): void {
    if ((e.target as HTMLElement).closest('[role="menu"], input')) return
    e.preventDefault()
    e.stopPropagation()
    if (!isSel(entry.name)) model.sel = { selected: [entry.name], anchor: entry.name, cursor: entry.name }
    else model.sel = { ...model.sel, cursor: entry.name }
    showMenu({ items: itemMenu(entry), at: point(e), anchor: e.currentTarget as HTMLElement })
  }

  function onblankmenu(e: MouseEvent): void {
    if ((e.target as HTMLElement).closest('[role="menu"], [role="option"], input')) return
    e.preventDefault()
    model.sel = { ...model.sel, selected: [] }
    showMenu({ items: blankMenu(), at: point(e), anchor: listEl })
  }

  /** Shift+F10 or the menu key: the menu of the cursor row, or of the folder. */
  function keyMenu(): void {
    const entry = model.visible.find((v) => v.name === model.sel.cursor)
    const row = entry ? document.getElementById(rowId(entry.name)) : null
    if (entry && row) {
      if (!isSel(entry.name)) model.sel = { selected: [entry.name], anchor: entry.name, cursor: entry.name }
      const r = row.getBoundingClientRect()
      showMenu({ items: itemMenu(entry), at: { x: r.left + 24, y: r.bottom }, anchor: row })
    } else if (listEl) {
      const r = listEl.getBoundingClientRect()
      showMenu({ items: blankMenu(), at: { x: r.left + 24, y: r.top + 8 }, anchor: listEl })
    }
  }

  // ---- keyboard ----

  const nav = new Set(['ArrowUp', 'ArrowDown', 'Home', 'End', 'PageUp', 'PageDown'])

  function onkeydown(e: KeyboardEvent): void {
    if ((e.target as HTMLElement).closest('input')) return
    const ctrl = e.ctrlKey && !e.altKey && !e.metaKey
    if (nav.has(e.key) && !e.altKey) {
      e.preventDefault()
      const page = Math.max(1, Math.floor((listEl?.clientHeight ?? 280) / 28) - 1)
      model.sel = keySelect(model.sel, model.order, e.key as NavKey, { ctrl: e.ctrlKey, shift: e.shiftKey }, page)
      void focusCursor()
    } else if (ctrl && !e.shiftKey && e.code === 'KeyA') {
      e.preventDefault()
      model.sel = selectAll(model.order)
    } else if (ctrl && !e.shiftKey && (e.code === 'KeyX' || e.code === 'KeyC')) {
      e.preventDefault()
      model.toClipboard([...model.sel.selected], e.code === 'KeyX')
    } else if (ctrl && !e.shiftKey && e.code === 'KeyV') {
      e.preventDefault()
      void model.paste()
    } else if (e.key === 'Enter' && !e.altKey) {
      const entry = model.visible.find((v) => v.name === model.sel.cursor) ?? model.selectedEntries[0]
      if (!entry) return
      e.preventDefault()
      const sel = model.selectedEntries
      if (sel.length > 1) void model.download(sel.map((s) => s.name))
      else void model.open(entry)
    } else if (e.key === 'Backspace' && !e.ctrlKey && !e.altKey) {
      e.preventDefault()
      void model.up()
    } else if (e.key === 'F2' && model.sel.selected.length === 1) {
      e.preventDefault()
      model.startRename(model.sel.selected[0])
    } else if (e.key === 'Delete' && model.sel.selected.length > 0) {
      e.preventDefault()
      model.askDelete([...model.sel.selected])
    } else if (ctrl && e.shiftKey && e.code === 'KeyC') {
      e.preventDefault()
      oncopypath(model.sel.selected.length > 0 ? model.sel.selected.map((n) => model.pathOf(n)) : [model.path])
    } else if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) {
      e.preventDefault()
      keyMenu()
    }
  }

  // ---- inline editors ----

  /** Mounts the rename field with the stem selected; Enter, Esc and leaving the field finish it. */
  function renameField(el: HTMLInputElement, entry: Entry) {
    el.value = entry.name
    el.focus()
    const [a, b] = stemRange(entry.name, entry.dir)
    el.setSelectionRange(a, b)
  }

  function newFolderField(el: HTMLInputElement) {
    el.value = 'Новая папка'
    el.focus()
    el.select()
  }

  let committing = false

  async function commitRename(el: HTMLInputElement, entry: Entry): Promise<void> {
    if (committing || model.renaming !== entry.name) return
    committing = true
    const ok = await model.rename(entry.name, el.value)
    committing = false
    if (ok) void focusCursor()
  }

  async function commitCreate(el: HTMLInputElement): Promise<void> {
    if (committing || !model.creating) return
    committing = true
    const ok = await model.mkdir(el.value)
    committing = false
    if (ok) void focusCursor()
  }

  function editKeys(e: KeyboardEvent, commit: () => void): void {
    e.stopPropagation()
    if (e.key === 'Enter') {
      e.preventDefault()
      commit()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      model.cancelEdits()
      void focusCursor().then(() => !model.sel.cursor && listEl?.focus())
    }
  }

  // ---- drag inside the server ----

  function ondragstart(e: DragEvent, entry: Entry): void {
    if (!e.dataTransfer || model.renaming) return
    if (!isSel(entry.name)) model.sel = { selected: [entry.name], anchor: entry.name, cursor: entry.name }
    const paths = model.sel.selected.map((n) => model.pathOf(n))
    model.dragging = paths
    e.dataTransfer.setData(MOVE_TYPE, JSON.stringify(paths))
    e.dataTransfer.effectAllowed = 'move'
    // Go watches the drag: out of the window it goes on as a system drag that Explorer copies from.
    void watchDragOut(model.id, paths)
  }

  function ondragover(e: DragEvent, entry: Entry): void {
    if (!e.dataTransfer?.types.includes(MOVE_TYPE)) return
    const target = joinPath(model.path, entry.name)
    if (!isFolder(entry) || model.dragging.includes(target)) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    over = entry.name
  }

  function ondragleave(e: DragEvent, entry: Entry): void {
    const to = e.relatedTarget as Node | null
    if (over === entry.name && !(to && (e.currentTarget as HTMLElement).contains(to))) over = ''
  }

  function ondrop(e: DragEvent, entry: Entry): void {
    if (!e.dataTransfer?.types.includes(MOVE_TYPE)) return
    e.preventDefault()
    over = ''
    const paths = model.dragging
    model.dragging = []
    void model.move(paths, model.pathOf(entry.name))
  }

  function ondragend(): void {
    model.dragging = []
    over = ''
  }
</script>

<div class="files">
  <div class="head" role="row">
    {#each columns as c (c.key)}
      <div class="col {c.cls}" role="columnheader" aria-sort={model.sort.key === c.key ? (model.sort.desc ? 'descending' : 'ascending') : 'none'}>
        <button type="button" onclick={() => model.setSort(c.key)}>
          <span>{c.label}</span>
          {#if model.sort.key === c.key}
            <span class="arrow" class:desc={model.sort.desc}><Icon name="chevron" size={12} /></span>
          {/if}
        </button>
      </div>
    {/each}
  </div>

  {#if model.slow}<div class="loading" aria-hidden="true"></div>{/if}

  <div
    class="list"
    role="listbox"
    aria-label="Содержимое папки"
    aria-multiselectable="true"
    tabindex={model.order.includes(model.sel.cursor) ? -1 : 0}
    bind:this={listEl}
    data-file-drop-target=""
    data-files-tab={model.id}
    data-files-dir={model.path}
    onclick={onblankclick}
    oncontextmenu={onblankmenu}
    {onkeydown}
  >
    {#if model.creating}
      <div class="row editing" role="option" aria-selected="true">
        <span class="c name">
          <span class="ico folder"><Icon name="folder" /></span>
          <span class="edit">
            <input
              type="text"
              aria-label="Имя новой папки"
              spellcheck="false"
              autocomplete="off"
              use:newFolderField
              oninput={() => (model.createError = '')}
              onkeydown={(e) => editKeys(e, () => void commitCreate(e.currentTarget as HTMLInputElement))}
              onblur={(e) => void commitCreate(e.currentTarget)}
            />
            {#if model.createError}<span class="tip" role="alert">{model.createError}</span>{/if}
          </span>
        </span>
      </div>
    {/if}

    {#each model.visible as entry (entry.name)}
      {@const folder = isFolder(entry)}
      {@const path = model.pathOf(entry.name)}
      <div
        id={rowId(entry.name)}
        class="row"
        class:sel={isSel(entry.name)}
        class:broken={entry.broken}
        class:hidden-entry={entry.name.startsWith('.')}
        class:cut={cutPaths.has(path)}
        class:over={over === entry.name}
        class:editing={model.renaming === entry.name}
        class:menu-open={menu?.anchor?.id === rowId(entry.name)}
        role="option"
        aria-selected={isSel(entry.name)}
        tabindex={model.sel.cursor === entry.name ? 0 : -1}
        draggable={model.renaming !== entry.name}
        title={entry.broken ? 'Ссылка никуда не ведёт' : undefined}
        data-file-drop-target={folder ? '' : undefined}
        data-files-tab={folder ? model.id : undefined}
        data-files-dir={folder ? path : undefined}
        onclick={(e) => onrowclick(e, entry)}
        onkeydown={(e) => onrowkey(e, entry)}
        ondblclick={() => model.renaming !== entry.name && void model.open(entry)}
        oncontextmenu={(e) => onrowmenu(e, entry)}
        ondragstart={(e) => ondragstart(e, entry)}
        ondragover={(e) => ondragover(e, entry)}
        ondragleave={(e) => ondragleave(e, entry)}
        ondrop={(e) => ondrop(e, entry)}
        {ondragend}
      >
        <span class="c name">
          <span class="ico" class:folder>
            <Icon name={folder ? 'folder' : 'file'} />
            {#if entry.link}<span class="badge" aria-label="Ссылка"><Icon name="link" size={9} /></span>{/if}
          </span>
          {#if model.renaming === entry.name}
            <span class="edit">
              <input
                type="text"
                aria-label="Новое имя"
                spellcheck="false"
                autocomplete="off"
                use:renameField={entry}
                oninput={() => (model.renameError = '')}
                onkeydown={(e) => editKeys(e, () => void commitRename(e.currentTarget as HTMLInputElement, entry))}
                onblur={(e) => void commitRename(e.currentTarget, entry)}
                onclick={(e) => e.stopPropagation()}
                ondblclick={(e) => e.stopPropagation()}
              />
              {#if model.renameError}<span class="tip" role="alert">{model.renameError}</span>{/if}
            </span>
          {:else}
            <span class="text" data-testid="file-name">{entry.name}</span>
          {/if}
        </span>
        <span class="c date">{formatDate(entry.mtime)}</span>
        <span class="c size">{entry.dir ? '' : formatSize(entry.size)}</span>
        <span class="c mode">{entry.mode}</span>
      </div>
    {/each}

    {#if model.listError}
      <div class="state" role="alert">
        <span class="state-ico"><Icon name="warning" size={18} /></span>
        <p class="state-title">{model.listError}</p>
        <p class="state-sub">Путь остаётся в строке: можно вернуться назад или подняться выше.</p>
      </div>
    {:else if model.visible.length === 0 && !model.creating}
      <div class="state">
        {#if model.slow || (model.listing && !model.loaded)}
          <p class="state-sub">Загрузка…</p>
        {:else if model.filter.trim()}
          <p class="state-sub">Ничего не найдено</p>
        {:else if model.loaded}
          <p class="state-sub">Папка пуста. Перетащите сюда файлы с компьютера</p>
        {/if}
      </div>
    {/if}
  </div>

  <div class="footer" aria-live="polite">
    <span>{footer}{#if model.hiddenCount > 0 && !model.listError}<span class="muted" title="Имена, начинающиеся с точки; кнопка «Скрытые» показывает их">{` · скрыто: ${model.hiddenCount}`}</span>{/if}</span>
    {#if model.slow}<span class="muted">Загрузка папки…</span>{/if}
  </div>
</div>

{#if menuOpen && lastMenu}
  <Menu items={lastMenu.items} at={lastMenu.at} anchor={lastMenu.anchor} onclose={() => (menuOpen = false)} />
{/if}

<style>
  .files {
    --cols: minmax(220px, 1fr) 136px 92px 104px;
    position: relative;
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }
  .head,
  .row {
    display: grid;
    grid-template-columns: var(--cols);
    align-items: center;
  }
  .head {
    flex: none;
    height: 30px;
    padding: 0 8px;
    border-bottom: 1px solid var(--border);
  }
  .col button {
    display: flex;
    align-items: center;
    gap: 4px;
    width: 100%;
    height: 26px;
    padding: 0 8px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    cursor: default;
    transition: background-color 80ms;
  }
  .col.size button {
    justify-content: flex-end;
  }
  .col button:hover {
    background: #22262d;
    color: var(--text);
  }
  .col button:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .arrow {
    display: grid;
    transform: rotate(180deg);
  }
  .arrow.desc {
    transform: none;
  }

  .loading {
    position: absolute;
    top: 30px;
    left: 0;
    right: 0;
    z-index: 1;
    height: 2px;
    overflow: hidden;
  }
  .loading::after {
    content: '';
    position: absolute;
    inset: 0 auto 0 0;
    width: 30%;
    background: var(--accent);
    animation: slide 1.1s ease-in-out infinite;
  }
  @keyframes slide {
    from {
      transform: translateX(-100%);
    }
    to {
      transform: translateX(340%);
    }
  }

  .list {
    position: relative;
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    padding: 4px 8px 8px;
    overflow: auto;
  }
  /* Keyboard focus is drawn on the current row, as in Explorer, never as a ring round the list. With
     the list itself focused (no current row yet), the first row, where the arrows start, shows it. */
  .list:focus-visible {
    outline: none;
  }
  .list:focus-visible > .row:first-child {
    outline: 1px solid var(--focus);
    outline-offset: -1px;
  }
  .list:global(.file-drop-target-active) {
    background: rgba(79, 143, 247, 0.06);
    box-shadow: inset 0 0 0 1px var(--accent);
  }

  .row {
    flex: none;
    position: relative;
    height: 28px;
    border-radius: var(--radius-control);
    color: var(--text);
    font-size: 13px;
    cursor: default;
    content-visibility: auto;
    contain-intrinsic-size: auto 28px;
    transition: background-color 80ms;
  }
  .row:hover {
    background: #22262d;
  }
  .row.sel,
  .row.menu-open {
    background: #263247;
  }
  .row.sel:hover {
    background: #2b3850;
  }
  .row:focus-visible {
    outline: 1px solid var(--focus);
    outline-offset: -1px;
  }
  .row.over,
  .row:global(.file-drop-target-active) {
    background: rgba(79, 143, 247, 0.2);
    box-shadow: inset 0 0 0 1px var(--accent);
  }
  .row.editing,
  .row.editing:hover {
    background: #263247;
  }
  /* The error under the field must not be clipped by the containment of the row. */
  .row.editing {
    z-index: 2;
    content-visibility: visible;
  }
  .row.broken,
  .row.hidden-entry {
    color: #7d8794;
  }
  .row.cut .ico,
  .row.cut .text {
    opacity: 0.5;
  }
  .row.broken .text {
    text-decoration: line-through;
    text-decoration-color: rgba(125, 135, 148, 0.6);
  }

  .c {
    min-width: 0;
    padding: 0 8px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .date,
  .size,
  .mode {
    color: var(--muted);
    font-size: 12px;
  }
  .row.broken .date,
  .row.broken .size,
  .row.broken .mode {
    color: #6f7a87;
  }
  .size {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .date {
    font-variant-numeric: tabular-nums;
  }
  .mode {
    font-family: var(--font-mono);
    font-size: 11.5px;
  }
  .name {
    display: flex;
    align-items: center;
    gap: 8px;
    overflow: visible;
  }
  .text {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .ico {
    position: relative;
    display: grid;
    flex: none;
    color: #8b95a3;
  }
  .ico.folder {
    color: #d8b25a;
  }
  .row.broken .ico {
    color: #5f6873;
  }
  .badge {
    position: absolute;
    left: -3px;
    bottom: -3px;
    display: grid;
    place-items: center;
    width: 11px;
    height: 11px;
    border-radius: 2px;
    background: var(--bg);
    color: var(--text);
  }

  .edit {
    position: relative;
    flex: 1;
    min-width: 0;
  }
  .edit input {
    width: 100%;
    max-width: 360px;
    height: 24px;
    padding: 0 6px;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
    color: var(--text);
    font: inherit;
    font-size: 13px;
    user-select: text;
  }
  .edit input:focus {
    outline: none;
    border-color: var(--accent);
  }
  .tip {
    position: absolute;
    top: 28px;
    left: 0;
    z-index: 3;
    max-width: 360px;
    padding: 6px 10px;
    background: #3a2624;
    border: 1px solid #5c3a35;
    border-radius: var(--radius-control);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
    color: var(--error);
    font-size: 12px;
    white-space: normal;
  }

  .state {
    display: flex;
    flex: 1;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 6px;
    min-height: 120px;
    padding: 24px;
    text-align: center;
  }
  .state p {
    margin: 0;
  }
  .state-ico {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    margin-bottom: 6px;
    border-radius: var(--radius-card);
    background: rgba(240, 140, 125, 0.14);
    color: var(--error);
  }
  .state-title {
    font-size: 14px;
    font-weight: 600;
    user-select: text;
  }
  .state-sub {
    color: var(--muted);
    font-size: 13px;
  }

  .footer {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    height: 26px;
    padding: 0 16px;
    border-top: 1px solid var(--border);
    color: var(--muted);
    font-size: 12px;
  }
  .muted {
    color: var(--muted);
  }
</style>
