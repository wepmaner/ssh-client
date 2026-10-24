<script lang="ts">
  import type { ErrorKind } from '../sessions.svelte'
  import { toast } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import { copyText } from './keys'

  let {
    hostName,
    kind = 'other',
    message,
    detail,
    retryable,
    onretry,
    onopenhost,
  }: {
    hostName: string
    kind?: ErrorKind
    message: string
    detail: string
    retryable: boolean
    onretry: () => void
    onopenhost: () => void
  } = $props()

  const explanations: Record<Exclude<ErrorKind, 'other'>, string> = {
    timeout: 'Сервер не ответил за 10 секунд. Чаще всего это выключенная машина, закрытый порт или фаервол между вами и сервером.',
    hostkey: 'Ключ сервера изменился. Возможна подмена сервера. Если сервер переустанавливали, удалите его строку из known_hosts.',
    auth: 'Доступ запрещён: сервер отклонил ключ и пароль.',
    cancelled: 'Подключение отменено.',
  }
  const explanation = $derived(kind === 'other' ? message : explanations[kind])

  async function copyError(): Promise<void> {
    await copyText(detail || message)
    toast('Ошибка скопирована')
  }
</script>

<div class="overlay">
  <div class="card" role="alert">
    <div class="tile" aria-hidden="true">
      <svg width="18" height="18" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
        <path d="M8 1.8 14.6 13.5H1.4z" />
        <path d="M8 6.2v3.4M8 11.6h.01" />
      </svg>
    </div>
    <h2>Не удалось подключиться к {hostName}</h2>
    <p>{explanation}</p>
    {#if detail}<pre>{detail}</pre>{/if}
    <div class="actions">
      <Button variant={retryable ? 'primary' : 'outline'} onclick={onretry}>Повторить</Button>
      <Button variant="outline" onclick={onopenhost}>Открыть хост</Button>
      <Button variant="ghost" onclick={copyError}>Скопировать ошибку</Button>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: absolute;
    inset: 0;
    z-index: 4;
    display: grid;
    place-items: center;
    padding: 24px;
    background: rgba(15, 17, 20, 0.72);
  }
  .card {
    width: 520px;
    max-width: 100%;
    padding: 24px;
    background: #1e2228;
    border: 1px solid #333a44;
    border-radius: 8px;
    box-shadow: 0 16px 48px rgba(0, 0, 0, 0.5);
    user-select: text;
  }
  .tile {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    margin-bottom: 14px;
    border-radius: 6px;
    background: rgba(240, 140, 125, 0.14);
    color: var(--error);
  }
  h2 {
    margin: 0 0 8px;
    font-size: 16px;
    font-weight: 600;
  }
  p {
    margin: 0 0 14px;
    color: var(--muted);
    font-size: 13px;
    line-height: 1.5;
  }
  pre {
    max-height: 140px;
    margin: 0 0 18px;
    padding: 10px 12px;
    overflow: auto;
    background: var(--terminal);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 12px;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
