<script lang="ts">
  import { KeyCopyPublic, KeyDelete, KeyExport, KeyRename, errorText, type app } from '../api'
  import { hosts, toast, vault } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'
  import PasswordDialog from '../ui/PasswordDialog.svelte'
  import { CANCELLED, formatDate, typeLabel } from './format'

  interface Props {
    key: app.KeyDTO
    /** Called after the key list changed on the Go side. */
    onchanged: () => Promise<void>
  }
  let { key, onchanged }: Props = $props()

  let renaming = $state(false)
  let draft = $state('')
  let renameEl: HTMLInputElement | undefined = $state()
  let confirmDelete = $state(false)
  let exportFormat = $state<string | null>(null)
  let error = $state('')

  const usedBy = $derived(key.host_ids.map((id) => hosts.find((h) => h.id === id)?.name ?? id))

  // Switching to another key abandons any half-done edit.
  $effect(() => {
    void key.id
    renaming = false
    error = ''
  })

  function startRename() {
    draft = key.name
    renaming = true
    error = ''
    queueMicrotask(() => renameEl?.select())
  }

  async function commitRename() {
    if (!renaming) return
    renaming = false
    const name = draft.trim()
    if (!name || name === key.name) return
    try {
      await KeyRename(key.id, name)
      await onchanged()
    } catch (e) {
      error = errorText(e)
    }
  }

  async function copy() {
    error = ''
    try {
      await KeyCopyPublic(key.id)
      toast('Публичный ключ скопирован')
    } catch (e) {
      error = errorText(e)
    }
  }

  async function remove() {
    confirmDelete = false
    error = ''
    try {
      await KeyDelete(key.id)
      await onchanged()
    } catch (e) {
      error = errorText(e)
    }
  }

  /** Throws on failure; a cancelled save dialog is silent. */
  async function doExport(format: string, password: string) {
    try {
      await KeyExport(key.id, format, password)
      toast('Ключ сохранён')
    } catch (e) {
      if (errorText(e) !== CANCELLED) throw e
    }
  }

  async function startExport(format: string) {
    error = ''
    if (vault.has_password) {
      exportFormat = format
      return
    }
    try {
      await doExport(format, '')
    } catch (e) {
      error = errorText(e)
    }
  }

  /** Opened from the context menu of the key list. */
  export function requestRename() {
    startRename()
  }
  export function requestDelete() {
    confirmDelete = true
  }
</script>

<section class="card">
  <header>
    {#if renaming}
      <Input
        bind:value={draft}
        bind:el={renameEl}
        aria-label="Название ключа"
        onkeydown={(e) => {
          if (e.key === 'Enter') commitRename()
          else if (e.key === 'Escape') {
            e.stopPropagation()
            renaming = false
          }
        }}
        onblur={commitRename}
        style="width:260px"
      />
    {:else}
      <h1>{key.name}</h1>
      <button type="button" class="icon-btn" title="Переименовать" aria-label="Переименовать" onclick={startRename}>
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-linejoin="round" aria-hidden="true">
          <path d="M9.5 2.5l2 2L5 11l-2.75.75L3 9z" />
          <path d="M8.25 3.75l2 2" />
        </svg>
      </button>
    {/if}
    <span class="chip">{typeLabel(key.type)}</span>
    {#if key.encrypted}<span class="chip">с парольной фразой</span>{/if}
    <span class="spacer"></span>
    <Button variant="ghost" onclick={() => (confirmDelete = true)} style="color:var(--error)">Удалить</Button>
  </header>
  {#if error}<p class="error" role="alert">{error}</p>{/if}

  <div class="block">
    <h2>Публичный ключ</h2>
    <div class="pub">
      <pre>{key.public_line}</pre>
      <button type="button" class="icon-btn" title="Скопировать" aria-label="Скопировать публичный ключ" onclick={copy}>
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" aria-hidden="true">
          <rect x="4.5" y="4.5" width="8" height="8" rx="1.5" />
          <path d="M9.5 2.5v-.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h.5" />
        </svg>
      </button>
    </div>
  </div>

  <dl>
    <dt>Отпечаток</dt>
    <dd class="mono">{key.fingerprint}</dd>
    <dt>Комментарий</dt>
    <dd class="mono">{key.comment || '—'}</dd>
    <dt>Создан</dt>
    <dd>{formatDate(key.created_at)}</dd>
    <dt>Хранение</dt>
    <dd>
      Хранилище приложения, {vault.has_password ? 'пароль' : 'защита Windows'}
      {#if key.encrypted}<span class="muted caption">Фраза спрашивается при подключении</span>{/if}
    </dd>
  </dl>

  <div class="block">
    <h2>Используется на хостах</h2>
    <div class="chips">
      {#each usedBy as n (n)}<span class="chip">{n}</span>{:else}<span class="muted">пока нет</span>{/each}
    </div>
  </div>

  <div class="block">
    <h2>Экспорт</h2>
    <div class="export">
      <Button variant="outline" onclick={() => startExport('public')}>Публичный ключ в файл</Button>
      {#if key.encrypted}
        <Button variant="outline" onclick={() => startExport('original')}>Приватный ключ (исходный файл)</Button>
      {:else}
        <Button variant="outline" onclick={() => startExport('openssh')}>Приватный ключ · OpenSSH</Button>
        <Button variant="outline" onclick={() => startExport('ppk')}>Приватный ключ · PuTTY .ppk</Button>
      {/if}
    </div>
    <p class="muted note">Экспорт приватного ключа спросит пароль хранилища, если шифрование включено</p>
  </div>
</section>

<Dialog open={confirmDelete} title="Удалить ключ {key.name}?" onclose={() => (confirmDelete = false)} width={420}>
  <p class="muted">Приватный ключ будет удалён из хранилища. Это действие нельзя отменить.</p>
  {#snippet footer()}
    <span></span>
    <span class="actions">
      <Button variant="ghost" onclick={() => (confirmDelete = false)}>Отмена</Button>
      <Button variant="primary" onclick={remove}>Удалить</Button>
    </span>
  {/snippet}
</Dialog>

<PasswordDialog
  open={exportFormat !== null}
  title="Экспорт ключа"
  hint="Введите пароль хранилища, чтобы сохранить ключ в файл."
  confirmLabel="Экспортировать"
  onsubmit={(pw) => doExport(exportFormat ?? '', pw)}
  onclose={() => (exportFormat = null)}
/>

<style>
  .card {
    flex: 1;
    min-width: 0;
    padding: 16px 18px;
    overflow: auto;
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
  }
  header {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 32px;
    margin-bottom: 14px;
  }
  h1 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  h2 {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
    font-weight: 600;
  }
  .spacer {
    flex: 1;
  }
  .chip {
    padding: 2px 8px;
    background: #2e333b;
    border-radius: var(--radius-control);
    font-size: 12px;
  }
  .block {
    margin-bottom: 18px;
  }
  .block > h2 {
    margin-bottom: 8px;
  }
  .icon-btn {
    display: grid;
    place-items: center;
    flex: none;
    width: 28px;
    height: 28px;
    padding: 0;
    border: none;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    cursor: default;
    transition: background-color 80ms;
  }
  .icon-btn:hover {
    background: #262b33;
    color: var(--text);
  }
  .icon-btn:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  header .icon-btn {
    margin-left: -6px;
  }
  /* The box is as wide as the key: a short Ed25519 line stays short, a long RSA one wraps inside. */
  .pub {
    display: flex;
    align-items: flex-start;
    gap: 4px;
    max-width: 100%;
  }
  pre {
    flex: 0 1 auto;
    width: fit-content;
    max-width: 100%;
    min-width: 0;
    margin: 0;
    padding: 10px 12px;
    background: #15181c;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font-family: var(--font-mono);
    font-size: 12px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    user-select: text;
  }
  dl {
    display: grid;
    grid-template-columns: 110px 1fr;
    gap: 8px 16px;
    margin: 0 0 18px;
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
    user-select: text;
  }
  .mono {
    font-family: var(--font-mono);
    font-size: 12px;
  }
  .chips,
  .export {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .muted {
    color: var(--muted);
  }
  .caption {
    display: block;
    margin-top: 2px;
    font-size: 12px;
  }
  .note {
    margin: 10px 0 0;
    font-size: 12px;
  }
  .error {
    margin: -6px 0 12px;
    color: var(--error);
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
