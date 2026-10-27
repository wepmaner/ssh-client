<script lang="ts">
  import { untrack } from 'svelte'
  import { ForwardSave, errorText, type app } from '../api'
  import { fieldError } from '../hosts/format'
  import { shortListen } from './format'
  import { hosts } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'
  import Segmented from '../ui/Segmented.svelte'
  import { refreshForwards } from './forwards.svelte'

  interface Props {
    open: boolean
    /** The forward to edit; null for a new one. */
    forward: app.ForwardDTO | null
    /** Host preselected for a new forward. */
    hostId?: string
    /** A forward to copy into a new one (forward is null): everything but the name. */
    template?: app.ForwardDTO | null
    onclose: () => void
  }
  let { open, forward, hostId = '', template = null, onclose }: Props = $props()

  type Kind = 'L' | 'R' | 'D'
  const explain: Record<Kind, string> = {
    L: 'Порт на этом компьютере ведёт на адрес за сервером',
    R: 'Порт на сервере ведёт сюда',
    D: 'SOCKS5-прокси для браузера и программ',
  }
  const listenHint: Record<Kind, string> = {
    L: '5432',
    R: '9000',
    D: '1080',
  }
  const targetHint: Record<Kind, string> = { L: 'localhost:5432', R: '127.0.0.1:3000', D: '' }

  let form = $state({ id: '', kind: 'L' as Kind, host_id: '', listen: '', target: '', name: '', auto_start: false })
  let errors = $state<Record<string, string>>({})
  let general = $state('')
  let busy = $state(false)

  function reset() {
    const f = forward
    const t = f ? null : template
    const src = f ?? t
    form = {
      id: f?.id ?? '',
      kind: (src?.kind as Kind) || 'L',
      host_id: src?.host_id || hostId || (hosts[0]?.id ?? ''),
      listen: shortListen(src?.listen ?? ''),
      target: src?.target ?? '',
      name: f?.name ?? '',
      auto_start: src?.auto_start ?? false,
    }
    errors = {}
    general = ''
    busy = false
  }

  $effect(() => {
    void forward
    void template
    if (open) untrack(reset)
  })

  /** The name Go gives an unnamed forward, shown as the placeholder. */
  const defaultName = $derived.by(() => {
    const listen = shortListen(form.listen.trim())
    if (!listen) return 'По умолчанию — из адресов'
    if (form.kind === 'D') return `SOCKS5 ${listen}`
    return `${form.kind} ${listen} → ${form.target.trim()}`
  })

  function clearError(field: string) {
    if (errors[field]) errors = { ...errors, [field]: '' }
  }

  function setKind(k: string) {
    form.kind = k as Kind
    clearError('kind')
    clearError('target')
  }

  async function save() {
    if (busy) return
    busy = true
    errors = {}
    general = ''
    try {
      await ForwardSave({
        id: form.id,
        name: form.name,
        kind: form.kind,
        host_id: form.host_id,
        host_name: '',
        listen: form.listen,
        target: form.kind === 'D' ? '' : form.target,
        auto_start: form.auto_start,
        status: '',
        error: '',
        detail: '',
        conns: 0,
        retryable: true,
      })
      await refreshForwards().catch(() => {})
      onclose()
    } catch (e) {
      const msg = errorText(e)
      const f = fieldError(msg)
      if (f) errors = { [f.field]: f.text }
      else general = msg
    } finally {
      busy = false
    }
  }

  function onfieldkey(e: KeyboardEvent) {
    const t = e.target as HTMLElement
    if (e.key !== 'Enter' || e.isComposing || t.tagName === 'BUTTON') return
    e.preventDefault()
    void save()
  }
</script>

{#snippet err(field: string)}
  {#if errors[field]}<span class="field-error" id="fwd-{field}-error" role="alert">{errors[field]}</span>{/if}
{/snippet}

<Dialog {open} title={form.id ? 'Изменить проброс' : 'Новый проброс'} {onclose} width={520}>
  <div class="form" role="presentation" onkeydown={onfieldkey}>
    <div class="field">
      <span class="label">Тип</span>
      <Segmented
        label="Тип проброса"
        options={[
          { value: 'L', label: 'L' },
          { value: 'R', label: 'R' },
          { value: 'D', label: 'D' },
        ]}
        value={form.kind}
        onchange={setKind}
      />
      <span class="hint">{explain[form.kind]}</span>
      {@render err('kind')}
    </div>
    <div class="field">
      <label for="fwd-host">Хост</label>
      <select
        id="fwd-host"
        class="select"
        bind:value={form.host_id}
        aria-describedby={errors.host ? 'fwd-host-error' : undefined}
        onchange={() => clearError('host')}
      >
        {#each hosts as h (h.id)}<option value={h.id}>{h.name}</option>{/each}
      </select>
      {@render err('host')}
    </div>
    <div class="grid" class:single={form.kind === 'D'}>
      <div class="field">
        <label for="fwd-listen">Слушать</label>
        <Input
          id="fwd-listen"
          bind:value={form.listen}
          placeholder={listenHint[form.kind]}
          autocomplete="off"
          spellcheck="false"
          style="font-family:var(--font-mono)"
          aria-invalid={!!errors.listen}
          aria-describedby={errors.listen ? 'fwd-listen-error' : undefined}
          oninput={() => clearError('listen')}
        />
        {#if errors.listen}
          {@render err('listen')}
        {:else}
          <span class="hint">Порт; адрес по умолчанию 127.0.0.1</span>
        {/if}
      </div>
      {#if form.kind !== 'D'}
        <div class="field">
          <label for="fwd-target">Назначение</label>
          <Input
            id="fwd-target"
            bind:value={form.target}
            placeholder={targetHint[form.kind]}
            autocomplete="off"
            spellcheck="false"
            style="font-family:var(--font-mono)"
            aria-invalid={!!errors.target}
            aria-describedby={errors.target ? 'fwd-target-error' : undefined}
            oninput={() => clearError('target')}
          />
          {@render err('target')}
        </div>
      {/if}
    </div>
    <div class="field">
      <label for="fwd-name">Название</label>
      <Input
        id="fwd-name"
        bind:value={form.name}
        placeholder={defaultName}
        autocomplete="off"
        aria-invalid={!!errors.name}
        aria-describedby={errors.name ? 'fwd-name-error' : undefined}
        oninput={() => clearError('name')}
      />
      {@render err('name')}
    </div>
    <label class="check">
      <input type="checkbox" bind:checked={form.auto_start} />
      Запускать при старте приложения
    </label>
  </div>

  {#snippet footer()}
    <span class="general">{#if general}<span class="field-error" role="alert">{general}</span>{/if}</span>
    <span class="actions">
      <Button variant="ghost" onclick={onclose}>Отмена</Button>
      <Button variant="primary" onclick={save} disabled={busy}>Сохранить</Button>
    </span>
  {/snippet}
</Dialog>

<style>
  .form {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
  }
  .grid.single {
    grid-template-columns: minmax(0, 1fr);
  }
  .field {
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 5px;
    min-width: 0;
  }
  .field > :global(.seg) {
    align-self: flex-start;
  }
  label,
  .label {
    color: var(--muted);
    font-size: 12px;
  }
  .check {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: var(--text);
    font-size: 13px;
  }
  .check input {
    margin: 0;
    accent-color: var(--accent);
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
