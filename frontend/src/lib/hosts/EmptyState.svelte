<script lang="ts">
  import RestoreButton from '../backup/RestoreButton.svelte'
  import Button from '../ui/Button.svelte'

  interface Props {
    onadd: () => void
    oncreatekey: () => void
  }
  let { onadd, oncreatekey }: Props = $props()
</script>

<div class="empty">
  <div class="content">
    <span class="icon" aria-hidden="true">
      <svg width="26" height="26" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.25">
        <rect x="2" y="2.5" width="12" height="4.5" rx="1.2" />
        <rect x="2" y="9" width="12" height="4.5" rx="1.2" />
        <path d="M4.5 4.75h.01M4.5 11.25h.01" stroke-width="2" stroke-linecap="round" />
      </svg>
    </span>
    <div class="text">
      <h1>Хостов пока нет</h1>
      <p>
        Для первого подключения нужны адрес сервера, имя пользователя и приватный ключ. Ключ можно создать здесь и
        скопировать его публичную часть на сервер.
      </p>
    </div>
    <div class="actions">
      <Button variant="primary" onclick={onadd}>Добавить хост</Button>
      <Button variant="outline" onclick={oncreatekey}>Сначала создать ключ</Button>
      <RestoreButton variant="ghost" />
    </div>
  </div>
</div>

<style>
  .empty {
    display: flex;
    flex: 1;
    align-items: center;
    justify-content: center;
    padding: 24px 0;
  }
  .content {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 18px;
    max-width: 440px;
    text-align: center;
  }
  .icon {
    display: grid;
    place-items: center;
    width: 56px;
    height: 56px;
    border-radius: 12px;
    background: var(--card);
    border: 1px solid var(--border);
    color: var(--muted);
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  h1 {
    margin: 0;
    font-size: 20px;
    font-weight: 600;
  }
  p {
    margin: 0;
    font-size: 13.5px;
    line-height: 1.55;
    color: var(--muted);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 8px;
  }
</style>
