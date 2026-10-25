<script lang="ts">
  import {
    KeyImportFile,
    KeyImportFileWithPassphrase,
    KeyImportText,
    KeyImportTextWithPassphrase,
    errorText,
    type app,
  } from '../api'
  import Button from '../ui/Button.svelte'
  import Input from '../ui/Input.svelte'
  import { CANCELLED, PASSPHRASE_PREFIX } from './format'

  interface Props {
    onimported: (key: app.KeyDTO) => void
  }
  let { onimported }: Props = $props()

  type Pending = { kind: 'file'; path: string } | { kind: 'text' }

  let tab = $state<'file' | 'text'>('file')
  let text = $state('')
  let name = $state('')
  let passphrase = $state('')
  let pending = $state<Pending | null>(null)
  let error = $state('')
  let busy = $state(false)

  function selectTab(t: 'file' | 'text') {
    tab = t
    pending = null
    passphrase = ''
    error = ''
  }

  /** Runs an import; only a legacy encrypted PEM switches the panel to the passphrase prompt. */
  async function run(call: () => Promise<app.KeyDTO>, onPassphrase: (ref: string) => Pending) {
    if (busy) return
    busy = true
    error = ''
    try {
      onimported(await call())
    } catch (e) {
      const msg = errorText(e)
      if (msg === CANCELLED) return
      if (msg.startsWith(PASSPHRASE_PREFIX)) {
        pending = onPassphrase(msg.slice(PASSPHRASE_PREFIX.length))
        passphrase = ''
      } else {
        error = msg
      }
    } finally {
      busy = false
    }
  }

  const pickFile = () => run(() => KeyImportFile(), (path) => ({ kind: 'file', path }))
  const importText = () => run(() => KeyImportText(text, name), () => ({ kind: 'text' }))

  function submitPassphrase() {
    const p = pending
    if (!p) return
    run(
      () => (p.kind === 'file' ? KeyImportFileWithPassphrase(p.path, passphrase) : KeyImportTextWithPassphrase(text, name, passphrase)),
      () => p,
    )
  }
</script>

<div class="panel">
  <div class="tabs" role="tablist">
    <button type="button" role="tab" aria-selected={tab === 'file'} class:on={tab === 'file'} onclick={() => selectTab('file')}>Файл</button>
    <button type="button" role="tab" aria-selected={tab === 'text'} class:on={tab === 'text'} onclick={() => selectTab('text')}>Вставить текст</button>
  </div>

  {#if pending}
    <form
      onsubmit={(e) => {
        e.preventDefault()
        submitPassphrase()
      }}
    >
      <p class="hint">Старый формат PEM прячет публичный ключ под парольной фразой. Введите её один раз, чтобы его прочитать: ключ сохранится вместе со своей фразой, сама фраза не запоминается.</p>
      <label>
        <span>Парольная фраза</span>
        <Input type="password" bind:value={passphrase} autocomplete="off" />
      </label>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <div class="row">
        <Button variant="ghost" onclick={() => ((pending = null), (error = ''))}>Назад</Button>
        <Button variant="primary" type="submit" disabled={!passphrase || busy}>Импортировать</Button>
      </div>
    </form>
  {:else if tab === 'file'}
    <div class="file">
      <Button variant="primary" onclick={pickFile} disabled={busy}>Выбрать файл…</Button>
      <p class="hint">id_ed25519 · id_rsa · .pem · .ppk</p>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
    </div>
  {:else}
    <form
      onsubmit={(e) => {
        e.preventDefault()
        importText()
      }}
    >
      <textarea bind:value={text} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" spellcheck="false" aria-label="Приватный ключ"></textarea>
      <label>
        <span>Название (необязательно)</span>
        <Input bind:value={name} autocomplete="off" />
      </label>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <div class="row">
        <span></span>
        <Button variant="primary" type="submit" disabled={!text.trim() || busy}>Импортировать</Button>
      </div>
    </form>
  {/if}
</div>

<style>
  .tabs {
    display: flex;
    gap: 4px;
    margin-bottom: 16px;
    border-bottom: 1px solid var(--border);
  }
  .tabs button {
    height: 32px;
    padding: 0 12px;
    border: 0;
    border-bottom: 2px solid transparent;
    background: transparent;
    color: var(--muted);
    font: inherit;
    cursor: default;
  }
  .tabs button.on {
    color: var(--text);
    border-bottom-color: var(--accent);
  }
  .tabs button:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .file {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
    padding: 8px 0 4px;
  }
  .hint {
    margin: 0;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.45;
  }
  form .hint {
    margin-bottom: 14px;
  }
  textarea {
    width: 100%;
    height: 140px;
    margin-bottom: 14px;
    padding: 8px 10px;
    resize: none;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
    color: var(--text);
    font-family: var(--font-mono);
    font-size: 12px;
    user-select: text;
  }
  textarea:focus {
    outline: none;
    border-color: var(--accent);
  }
  label span {
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
  .row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 16px;
  }
</style>
