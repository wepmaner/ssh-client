<script lang="ts">
  import { onMount } from 'svelte'
  import {
    HostsExport,
    ImportPuTTY,
    ImportSSHConfig,
    OpenDataFolder,
    SaveSettings,
    VaultBackup,
    VaultRemovePassword,
    Version,
    errorText,
    model,
    type app,
  } from '../api'
  import { refreshHosts, refreshSettings, refreshVault, settings, toast, vault } from '../stores.svelte'
  import { fieldError } from '../hosts/format'
  import { CANCELLED } from '../keys/format'
  import BackupDialog from '../backup/BackupDialog.svelte'
  import RestoreButton from '../backup/RestoreButton.svelte'
  import Button from '../ui/Button.svelte'
  import PasswordDialog from '../ui/PasswordDialog.svelte'
  import McpSection from './McpSection.svelte'
  import VaultDialog from './VaultDialog.svelte'

  const fonts = ['Cascadia Mono', 'JetBrains Mono', 'Consolas']
  const sizes = Array.from({ length: 11 }, (_, i) => 10 + i)
  const lockOptions = [
    { value: 0, label: 'Не блокировать' },
    { value: 5, label: '5 минут' },
    { value: 15, label: '15 минут' },
    { value: 30, label: '30 минут' },
  ]

  let version = $state('')
  let encrypting = $state(false)
  let decrypting = $state(false)
  let importing = $state(false)
  let backingUp = $state(false)
  let backupBusy = $state(false)
  let warnings = $state<string[]>([])

  onMount(() => {
    Version()
      .then((v) => (version = v))
      .catch(() => {})
  })

  /** Refusals of SaveSettings by field (font, size, lock), shown under the control. */
  let fieldErrors = $state<Record<string, string>>({})

  /**
   * Applies a change at once (open terminals follow the store) and saves it. A refusal shows
   * under its field (other errors as a toast) and reloads the stored settings.
   */
  function update(patch: Partial<model.Settings>): void {
    Object.assign(settings, patch)
    SaveSettings(model.Settings.createFrom(JSON.parse(JSON.stringify(settings))))
      .then(() => (fieldErrors = {}))
      .catch((e) => {
        const msg = errorText(e)
        const fe = fieldError(msg)
        if (fe) fieldErrors = { [fe.field]: fe.text }
        else toast(msg)
        void refreshSettings().catch(() => {})
      })
  }

  const describedBy = (field: string) => (fieldErrors[field] ? `set-${field}-error` : undefined)

  async function disableEncryption(password: string): Promise<void> {
    await VaultRemovePassword(password)
    await refreshVault()
  }

  async function runImport(fn: () => Promise<app.ImportResult>): Promise<void> {
    if (importing) return
    importing = true
    try {
      const res = await fn()
      warnings = res.warnings ?? []
      toast(`Добавлено хостов: ${res.added}, пропущено: ${res.skipped}`)
      await refreshHosts().catch(() => {})
    } catch (e) {
      warnings = []
      toast(errorText(e))
    } finally {
      importing = false
    }
  }

  async function exportHosts(): Promise<void> {
    try {
      await HostsExport()
      toast('Хосты экспортированы')
    } catch (e) {
      if (errorText(e) !== CANCELLED) toast(errorText(e))
    }
  }

  const backupSaved = (path: string) => toast(`Резервная копия сохранена: ${path.split(/[\\/]/).pop()}`)

  /** A password vault is copied under its own password at once; without one, the dialog asks a password for the copy. */
  async function backup(): Promise<void> {
    if (!vault.has_password) {
      backingUp = true
      return
    }
    if (backupBusy) return
    backupBusy = true
    try {
      const path = await VaultBackup('')
      if (path) backupSaved(path)
    } catch (e) {
      toast(errorText(e))
    } finally {
      backupBusy = false
    }
  }

  function openFolder(): void {
    OpenDataFolder().catch((e) => toast(errorText(e)))
  }
</script>

{#snippet err(field: string)}
  {#if fieldErrors[field]}<span class="field-error" id="set-{field}-error" role="alert">{fieldErrors[field]}</span>{/if}
{/snippet}

<div class="view">
  <section class="card" aria-labelledby="set-terminal">
    <h2 id="set-terminal">Терминал</h2>
    <div class="grid">
      <label class="field">
        <span>Шрифт</span>
        <select class="select" aria-describedby={describedBy('font')} value={settings.terminal_font} onchange={(e) => update({ terminal_font: e.currentTarget.value })}>
          {#each fonts as f (f)}<option value={f}>{f}</option>{/each}
        </select>
        {@render err('font')}
      </label>
      <label class="field">
        <span>Размер</span>
        <select class="select mono" aria-describedby={describedBy('size')} value={String(settings.terminal_size)} onchange={(e) => update({ terminal_size: Number(e.currentTarget.value) })}>
          {#each sizes as s (s)}<option value={String(s)}>{s}</option>{/each}
        </select>
        {@render err('size')}
      </label>
    </div>
    <label class="check">
      <input type="checkbox" checked={settings.copy_on_select} onchange={(e) => update({ copy_on_select: e.currentTarget.checked })} />
      Копировать при выделении
    </label>
    <label class="check">
      <input type="checkbox" checked={settings.blink_cursor} onchange={(e) => update({ blink_cursor: e.currentTarget.checked })} />
      Мигающий курсор
    </label>
    <label class="check">
      <input type="checkbox" checked={settings.webgl} onchange={(e) => update({ webgl: e.currentTarget.checked })} />
      Ускорение отрисовки (WebGL)
    </label>
  </section>

  <section class="card" aria-labelledby="set-storage">
    <h2 id="set-storage">Хранилище и безопасность</h2>
    <div class="status">
      <span class="tile" class:ok={vault.has_password} aria-hidden="true">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3.5" y="7" width="9" height="6.5" rx="1" />
          {#if vault.has_password}<path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />{:else}<path d="M5.5 7V5a2.5 2.5 0 0 1 4.7-1.2" />{/if}
        </svg>
      </span>
      <span class="status-text">
        {#if vault.has_password}
          <strong>Хранилище зашифровано паролем</strong>
          <span class="muted">Пароль спрашивается при запуске. Забытый пароль восстановить нельзя.</span>
        {:else}
          <strong>Шифрование паролем выключено</strong>
          <span class="muted">Ключи и хосты защищены учётной записью Windows</span>
        {/if}
      </span>
      {#if vault.has_password}
        <Button variant="outline" onclick={() => (decrypting = true)}>Отключить шифрование</Button>
      {:else}
        <Button variant="outline" onclick={() => (encrypting = true)}>Включить шифрование</Button>
      {/if}
    </div>
    {#if !vault.has_password}
      <p class="hint" role="note">
        Без пароля хранилище открывается только этой учётной записью Windows. Чтобы не потерять ключи при
        переустановке, включите пароль или сделайте резервную копию.
      </p>
    {/if}
    <div class="grid">
      <label class="field">
        <span>Блокировать после бездействия</span>
        <select
          class="select"
          disabled={!vault.has_password}
          aria-describedby={describedBy('lock')}
          value={String(vault.has_password ? settings.lock_after_min : 0)}
          onchange={(e) => update({ lock_after_min: Number(e.currentTarget.value) })}
        >
          {#each lockOptions as o (o.value)}<option value={String(o.value)}>{o.label}</option>{/each}
        </select>
        {@render err('lock')}
      </label>
    </div>
    {#if !vault.has_password}
      <p class="hint">Блокировка доступна, когда хранилище зашифровано паролем.</p>
    {/if}
    <div class="row">
      <Button variant="outline" disabled={backupBusy} onclick={backup}>Сделать резервную копию</Button>
      <RestoreButton />
    </div>
    <div class="row">
      <Button variant="outline" onclick={openFolder}>Открыть папку с данными</Button>
      <Button variant="outline" onclick={exportHosts}>Экспорт хостов в файл</Button>
    </div>
  </section>

  <McpSection />

  <section class="card" aria-labelledby="set-import">
    <h2 id="set-import">Импорт</h2>
    <div class="row">
      <Button variant="outline" disabled={importing} onclick={() => runImport(ImportSSHConfig)}>Хосты из ~/.ssh/config</Button>
      <Button variant="outline" disabled={importing} onclick={() => runImport(ImportPuTTY)}>Сессии из PuTTY</Button>
      <span class="hint">Ключи импортируются в разделе «Ключи»</span>
    </div>
    {#if warnings.length > 0}
      <ul class="warnings" aria-label="Предупреждения импорта">
        {#each warnings as w, i (i)}<li>{w}</li>{/each}
      </ul>
    {/if}
  </section>

  {#if version}<p class="version">Версия {version}</p>{/if}
</div>

<VaultDialog open={encrypting} onclose={() => (encrypting = false)} onenabled={refreshVault} />

<BackupDialog open={backingUp} onclose={() => (backingUp = false)} onsaved={backupSaved} />

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
    gap: 12px;
    max-width: 820px;
    height: 100%;
    padding: 16px 24px 24px;
    overflow: auto;
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 16px 18px;
    background: var(--card);
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
  }
  h2 {
    margin: 0;
    font-size: 14px;
    font-weight: 600;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px 16px;
    max-width: 460px;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 5px;
  }
  .field > span {
    font-size: 12px;
    color: var(--muted);
  }
  .select {
    height: var(--control-height);
    padding: 0 8px;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
    color: var(--text);
    font: inherit;
  }
  .select:focus {
    outline: none;
    border-color: var(--accent);
  }
  .select:disabled {
    opacity: 0.45;
  }
  .mono {
    font-family: var(--font-mono);
  }
  .check {
    display: flex;
    align-items: center;
    gap: 10px;
    width: fit-content;
  }
  .check input {
    margin: 0;
    accent-color: var(--accent);
  }
  .status {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px;
    padding: 10px 12px;
    background: #1e2228;
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
  }
  .tile {
    display: grid;
    place-items: center;
    flex: none;
    width: 28px;
    height: 28px;
    border-radius: var(--radius-card);
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
  }
  .status-text strong {
    font-weight: 500;
  }
  .muted {
    color: var(--muted);
    font-size: 12px;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .hint {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
  }
  .warnings {
    margin: 0;
    padding: 10px 12px 10px 28px;
    background: #1e2228;
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
    color: var(--muted);
    font-size: 12px;
    line-height: 1.6;
    user-select: text;
  }
  .field-error {
    color: var(--error);
    font-size: 12px;
  }
  .version {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
  }
</style>
