<script lang="ts">
  import { untrack } from 'svelte'
  import { GroupSave, HostSave, errorText, type app } from '../api'
  import { typeLabel } from '../keys/format'
  import KeyImportPanel from '../keys/KeyImportPanel.svelte'
  import { openSession } from '../sessions.svelte'
  import { groups, keys, refreshHosts, refreshKeys, ui } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'
  import Segmented from '../ui/Segmented.svelte'
  import { fieldError } from './format'

  interface Props {
    open: boolean
    /** The host to edit; without an id it is a new host prefilled with these values (a duplicate). */
    host: app.HostDTO | null
    onclose: () => void
  }
  let { open, host, onclose }: Props = $props()

  const NEW_GROUP = '__new'

  let form = $state({ id: '', address: '', port: '22', user: '', name: '', group_id: '', key_id: '', os: '', mcp_access: '' })
  let newGroup = $state('')
  let keyMode = $state<'store' | 'import'>('store')
  let errors = $state<Record<string, string>>({})
  let general = $state('')
  let busy = $state(false)

  function reset() {
    const h = host
    form = {
      id: h?.id ?? '',
      address: h?.address ?? '',
      port: String(h?.port || 22),
      user: h?.user ?? '',
      name: h?.name ?? '',
      group_id: h?.group_id ?? '',
      // A new host starts with the first key; an existing host or a copy keeps its own, which may be
      // none: then the server asks for the password in the console.
      key_id: h ? h.key_id : (keys[0]?.id ?? ''),
      os: h?.os ?? '',
      // A copy starts closed to MCP: opening a host to MCP is a deliberate act for each host.
      mcp_access: h?.id ? (h.mcp_access ?? '') : '',
    }
    newGroup = ''
    keyMode = keys.length > 0 || (h && !h.key_id) ? 'store' : 'import'
    errors = {}
    general = ''
    busy = false
  }

  $effect(() => {
    void host
    if (open) untrack(reset)
  })

  /** Port text as Go sees it: empty means the default, anything not a number fails validation there. */
  function portValue(text: string): number {
    const t = text.trim()
    if (t === '') return 0
    return /^\d+$/.test(t) ? Number(t) : -1
  }

  function showError(e: unknown) {
    const msg = errorText(e)
    const f = fieldError(msg)
    if (f) errors = { [f.field]: f.text }
    else general = msg
  }

  function clearError(field: string) {
    if (errors[field]) errors = { ...errors, [field]: '' }
  }

  /** Saves the form; resolves to the saved host or null when something is wrong. */
  async function save(): Promise<app.HostDTO | null> {
    if (busy) return null
    busy = true
    errors = {}
    general = ''
    try {
      if (form.group_id === NEW_GROUP) {
        try {
          form.group_id = (await GroupSave(newGroup)).id
          await refreshHosts()
        } catch (e) {
          // GroupSave names its own "name" field; here that field is the new group input.
          const msg = errorText(e)
          errors = { group: fieldError(msg)?.text ?? msg }
          return null
        }
      }
      const saved = await HostSave({
        id: form.id,
        name: form.name,
        address: form.address,
        port: portValue(form.port),
        user: form.user,
        group_id: form.group_id,
        group_name: '',
        key_id: form.key_id,
        key_name: '',
        os: form.os,
        mcp_access: form.mcp_access,
        connected: false,
      })
      await Promise.all([refreshHosts(), refreshKeys()])
      return saved
    } catch (e) {
      showError(e)
      return null
    } finally {
      busy = false
    }
  }

  async function submitSave() {
    if (await save()) onclose()
  }

  async function submitConnect() {
    const h = await save()
    if (!h) return
    onclose()
    void openSession(h.id)
  }

  function onfieldkey(e: KeyboardEvent) {
    const t = e.target as HTMLElement
    if (e.key !== 'Enter' || e.isComposing || t.tagName === 'BUTTON') return
    e.preventDefault()
    void submitConnect()
  }

  async function imported(k: app.KeyDTO) {
    await refreshKeys()
    form.key_id = k.id
    keyMode = 'store'
    clearError('key')
  }

  function allKeys() {
    onclose()
    ui.view = 'keys'
  }
</script>

{#snippet err(field: string)}
  {#if errors[field]}<span class="field-error" id="host-{field}-error" role="alert">{errors[field]}</span>{/if}
{/snippet}

<Dialog {open} title={form.id ? 'Изменить хост' : 'Новый хост'} {onclose} width={560}>
  <div class="form">
    <div class="fields" role="presentation" onkeydown={onfieldkey}>
      <div class="line">
        <div class="field grow">
          <label for="host-address">Адрес</label>
          <Input
            id="host-address"
            bind:value={form.address}
            placeholder="host.example.org или IP"
            autocomplete="off"
            spellcheck="false"
            style="font-family:var(--font-mono)"
            aria-invalid={!!errors.address}
            aria-describedby={errors.address ? 'host-address-error' : undefined}
            oninput={() => clearError('address')}
          />
          {@render err('address')}
        </div>
        <div class="field port">
          <label for="host-port">Порт</label>
          <Input
            id="host-port"
            bind:value={form.port}
            inputmode="numeric"
            autocomplete="off"
            style="font-family:var(--font-mono)"
            aria-invalid={!!errors.port}
            aria-describedby={errors.port ? 'host-port-error' : undefined}
            oninput={() => clearError('port')}
          />
          {@render err('port')}
        </div>
      </div>
      <div class="grid">
        <div class="field">
          <label for="host-user">Пользователь</label>
          <Input
            id="host-user"
            bind:value={form.user}
            placeholder="root"
            autocomplete="off"
            spellcheck="false"
            style="font-family:var(--font-mono)"
            aria-invalid={!!errors.user}
            aria-describedby={errors.user ? 'host-user-error' : undefined}
            oninput={() => clearError('user')}
          />
          {@render err('user')}
        </div>
        <div class="field">
          <label for="host-name">Название</label>
          <Input
            id="host-name"
            bind:value={form.name}
            placeholder={form.address.trim() || 'По умолчанию — адрес'}
            autocomplete="off"
            aria-invalid={!!errors.name}
            aria-describedby={errors.name ? 'host-name-error' : undefined}
            oninput={() => clearError('name')}
          />
          {@render err('name')}
        </div>
      </div>
      <div class="grid">
        <div class="field">
          <label for="host-group">Группа</label>
          <select
            id="host-group"
            class="select"
            bind:value={form.group_id}
            aria-describedby={errors.group && form.group_id !== NEW_GROUP ? 'host-group-error' : undefined}
            onchange={() => clearError('group')}
          >
            <option value="">Без группы</option>
            {#each groups as g (g.id)}<option value={g.id}>{g.name}</option>{/each}
            <option value={NEW_GROUP}>Новая группа…</option>
          </select>
          {#if form.group_id !== NEW_GROUP}{@render err('group')}{/if}
        </div>
        {#if form.group_id === NEW_GROUP}
          <div class="field">
            <label for="host-new-group">Название группы</label>
            <Input
              id="host-new-group"
              bind:value={newGroup}
              autocomplete="off"
              aria-invalid={!!errors.group}
              aria-describedby={errors.group ? 'host-group-error' : undefined}
              oninput={() => clearError('group')}
            />
            {@render err('group')}
          </div>
        {/if}
      </div>
    </div>

    <div class="field mcp">
      <label for="host-mcp">Доступ по MCP</label>
      <select
        id="host-mcp"
        class="select"
        bind:value={form.mcp_access}
        aria-describedby={errors.mcp_access ? 'host-mcp_access-error' : undefined}
        onchange={() => clearError('mcp_access')}
      >
        <option value="">Закрыт</option>
        <option value="read">Только просмотр</option>
        <option value="full">Полный</option>
      </select>
      {#if form.mcp_access === 'full'}
        <span class="hint">Полный доступ — всё, что может пользователь этого хоста на сервере</span>
      {/if}
      {@render err('mcp_access')}
    </div>

    <div class="field">
      <span class="label">Приватный ключ</span>
      <div class="key-card">
        <div class="key-head">
          <Segmented
            label="Откуда взять ключ"
            options={[
              { value: 'store', label: 'Из хранилища' },
              { value: 'import', label: 'Импортировать' },
            ]}
            value={keyMode}
            onchange={(v) => (keyMode = v as 'store' | 'import')}
          />
        </div>
        {#if keyMode === 'store'}
          <div class="key-store">
            <select
              class="select key-select"
              aria-label="Ключ"
              bind:value={form.key_id}
              aria-describedby={errors.key ? 'host-key-error' : undefined}
              onchange={() => clearError('key')}
            >
              <option value="">Без ключа — сервер спросит пароль</option>
              {#each keys as k (k.id)}
                <option value={k.id}>{k.name} · {typeLabel(k.type)}{k.encrypted ? ' · с фразой' : ''}</option>
              {/each}
            </select>
            {#if keys.length === 0}
              <span class="hint">В хранилище нет ключей: импортируйте ключ или создайте его в разделе «Ключи»</span>
            {/if}
            <button type="button" class="link" onclick={allKeys}>Все ключи</button>
          </div>
        {:else}
          <div class="key-import">
            <KeyImportPanel onimported={imported} />
            <span class="hint">Ключ попадёт в раздел «Ключи» под именем файла, там его можно переименовать</span>
          </div>
        {/if}
      </div>
      {@render err('key')}
      <span class="hint">Пароль или фразу ключа, если сервер их попросит, вы введёте в консоли</span>
    </div>
  </div>

  {#snippet footer()}
    <span class="general">{#if general}<span class="field-error" role="alert">{general}</span>{/if}</span>
    <span class="actions">
      <Button variant="ghost" onclick={onclose}>Отмена</Button>
      <Button variant="outline" onclick={submitSave} disabled={busy}>Сохранить</Button>
      <Button variant="primary" onclick={submitConnect} disabled={busy}>Подключиться</Button>
    </span>
  {/snippet}
</Dialog>

<style>
  .form,
  .fields {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .line {
    display: flex;
    gap: 10px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 5px;
    min-width: 0;
  }
  .mcp .select {
    max-width: 240px;
  }
  .grow {
    flex: 1;
  }
  .port {
    flex: 0 0 84px;
  }
  label,
  .label {
    color: var(--muted);
    font-size: 12px;
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
  .key-card {
    overflow: hidden;
    background: #181b20;
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
  }
  .key-head {
    padding: 4px;
    background: #15181c;
    border-bottom: 1px solid var(--border);
  }
  .key-store {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px;
  }
  .key-select {
    flex: 0 1 300px;
    min-width: 0;
  }
  .key-import {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 10px 12px 12px;
  }
  .link {
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    white-space: nowrap;
    cursor: default;
  }
  .link:hover {
    color: var(--text);
  }
  .link:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .hint {
    color: var(--muted);
    font-size: 11.5px;
  }
  .field-error {
    color: var(--error);
    font-size: 12px;
  }
  .general {
    min-width: 0;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
