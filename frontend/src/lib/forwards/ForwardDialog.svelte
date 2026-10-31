<script lang="ts">
  import { untrack } from 'svelte'
  import { ForwardSave, errorText, type app } from '../api'
  import { fieldError } from '../hosts/format'
  import { shortListen } from './format'
  import { hosts } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'
  import Select from '../ui/Select.svelte'
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
  const listenLabel: Record<Kind, string> = { L: 'Порт здесь', R: 'Порт на сервере', D: 'Порт здесь' }
  const listenNote: Record<Kind, string> = { L: 'на 127.0.0.1', R: 'слушает сервер', D: 'на 127.0.0.1' }
  const targetNote: Record<Kind, string> = {
    L: 'адрес:порт, как его видит сервер',
    R: 'адрес:порт на этом компьютере',
    D: '',
  }

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

  const hostName = $derived(hosts.find((h) => h.id === form.host_id)?.name ?? '')
  /** What the forward does, as a chain: shown once the port is filled. */
  const preview = $derived.by(() => {
    const listen = shortListen(form.listen.trim())
    if (!listen) return null
    const port = listen.slice(listen.lastIndexOf(':') + 1)
    const here = /^\d+$/.test(listen) ? `localhost:${listen}` : listen
    const target = form.target.trim() || '…'
    // An empty part (a host that is gone) is skipped together with its arrow.
    const chain = (parts: string[], arrow: string, note = '') => ({
      parts: parts.map((text, i) => ({ text, via: i === 1 })).filter((p) => p.text),
      arrow,
      note,
    })
    if (form.kind === 'D') return chain([here, hostName], '→', 'SOCKS5')
    if (form.kind === 'R') return chain([target, hostName, `порт ${port} на сервере`], '←')
    return chain([here, hostName, target], '→')
  })
  /** For a copy: the original is named in the subtitle. */
  const copyOf = $derived(!forward && template ? template.name : '')

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
    {#if copyOf}<p class="sub">Копия «{copyOf}»: поменяйте порт и сохраните</p>{/if}
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
      <Select
        id="fwd-host"
        value={form.host_id}
        options={hosts.map((h) => ({ value: h.id, label: h.name }))}
        aria-describedby={errors.host ? 'fwd-host-error' : undefined}
        onchange={(v) => {
          form.host_id = v
          clearError('host')
        }}
      />
      {@render err('host')}
    </div>
    <div class="grid" class:single={form.kind === 'D'}>
      <div class="field">
        <label for="fwd-listen">{listenLabel[form.kind]}</label>
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
          <span class="hint">{listenNote[form.kind]}</span>
        {/if}
      </div>
      {#if form.kind !== 'D'}
        <div class="field">
          <label for="fwd-target">Куда ведёт</label>
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
          {#if errors.target}
            {@render err('target')}
          {:else}
            <span class="hint">{targetNote[form.kind]}</span>
          {/if}
        </div>
      {/if}
    </div>
    {#if preview}
      <div class="preview" data-testid="route-preview">
        {#each preview.parts as part, i (i)}
          {#if i > 0}<span class="arrow" aria-hidden="true">{preview.arrow}</span>{/if}
          <span class:via={part.via}>{part.text}</span>
        {/each}
        {#if preview.note}<span class="via">({preview.note})</span>{/if}
      </div>
    {/if}
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
    grid-template-columns: 150px minmax(0, 1fr);
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
  .sub {
    margin: -6px 0 0;
    color: var(--muted);
    font-size: 12px;
  }
  .preview {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 10px;
    padding: 10px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius-card);
    color: #c9d1d9;
    font-family: var(--font-mono);
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .preview .via {
    color: var(--muted);
    font-family: var(--font-ui);
  }
  .preview .arrow {
    color: #6f7a87;
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
