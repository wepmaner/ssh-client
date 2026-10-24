<script lang="ts">
  import { KeyGenerate, errorText, type app } from '../api'
  import Button from '../ui/Button.svelte'
  import Dialog from '../ui/Dialog.svelte'
  import Input from '../ui/Input.svelte'
  import Segmented from '../ui/Segmented.svelte'

  interface Props {
    open: boolean
    onclose: () => void
    oncreated: (key: app.KeyDTO) => void
  }
  let { open, onclose, oncreated }: Props = $props()

  const hints: Record<string, string> = {
    ed25519: 'Рекомендуется: короткий, быстрый, поддерживается всеми современными серверами',
    rsa: 'Для старых серверов, которые не понимают Ed25519',
  }

  let name = $state('')
  let kind = $state('ed25519')
  let comment = $state('')
  let error = $state('')
  let busy = $state(false)

  $effect(() => {
    if (!open) return
    name = ''
    kind = 'ed25519'
    comment = ''
    error = ''
    busy = false
  })

  async function submit() {
    if (!name.trim() || busy) return
    busy = true
    error = ''
    try {
      const key = await KeyGenerate(name, kind, comment)
      oncreated(key)
      onclose()
    } catch (e) {
      error = errorText(e)
    } finally {
      busy = false
    }
  }
</script>

<Dialog {open} title="Новый ключ" {onclose} width={480}>
  <form
    id="key-create-form"
    onsubmit={(e) => {
      e.preventDefault()
      submit()
    }}
  >
    <label class="field">
      <span>Название</span>
      <Input bind:value={name} autocomplete="off" />
    </label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}

    <div class="field">
      <span>Тип</span>
      <Segmented
        label="Тип"
        value={kind}
        onchange={(v) => (kind = v)}
        options={[
          { value: 'ed25519', label: 'Ed25519' },
          { value: 'rsa', label: 'RSA 4096' },
        ]}
      />
      <p class="hint">{hints[kind]}</p>
    </div>

    <label class="field">
      <span>Комментарий</span>
      <Input bind:value={comment} class="mono" placeholder="По умолчанию — пользователь@компьютер" autocomplete="off" />
    </label>

    <div class="info" role="note">
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <rect x="3.5" y="7" width="9" height="6.5" rx="1" />
        <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
      </svg>
      <span>Приватный ключ останется в хранилище приложения. После создания скопируйте публичный ключ и добавьте его на сервер.</span>
    </div>
  </form>
  {#snippet footer()}
    <span></span>
    <span class="actions">
      <Button variant="ghost" onclick={onclose}>Отмена</Button>
      <Button variant="primary" type="submit" form="key-create-form" disabled={!name.trim() || busy}>Создать</Button>
    </span>
  {/snippet}
</Dialog>

<style>
  .field {
    display: block;
    margin-bottom: 16px;
  }
  .field > span {
    display: block;
    margin-bottom: 6px;
    color: var(--muted);
    font-size: 12px;
  }
  .field :global(.mono) {
    font-family: var(--font-mono);
    font-size: 12px;
  }
  .hint {
    margin: 8px 0 0;
    color: var(--muted);
    font-size: 12px;
  }
  .error {
    margin: -10px 0 16px;
    color: var(--error);
    font-size: 12px;
  }
  .info {
    display: flex;
    gap: 10px;
    padding: 10px 12px;
    background: #22262d;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    color: var(--muted);
    line-height: 1.45;
  }
  .info svg {
    flex: none;
    margin-top: 2px;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
