<script lang="ts">
  import { FilesLoginWrite } from '../api'
  import MiniTerminal from '../forwards/MiniTerminal.svelte'
  import Button from '../ui/Button.svelte'
  import type { FilesTab } from './files.svelte'
  import Icon from './Icon.svelte'

  /** «Вход на <хост>»: the server asks its questions in a small terminal, as for a forward. */
  let { model, hostName, onclose }: { model: FilesTab; hostName: string; onclose: () => void } = $props()
</script>

<div class="overlay">
  <div class="card" role="group" aria-label="Вход на {hostName}">
    <div class="head">
      <span class="tile" aria-hidden="true"><Icon name="folder" size={18} /></span>
      <div>
        <h2>Вход на {hostName}</h2>
        <p>Если сервер спросит пароль или фразу ключа, ответьте здесь. Ответ хранится только в памяти, пока подключение открыто.</p>
      </div>
    </div>
    {#key model.loginEpoch}
      <MiniTerminal write={(b64) => FilesLoginWrite(model.id, b64)} attach={(sink) => model.attachLogin(sink)} />
    {/key}
    <div class="actions">
      <Button variant="ghost" onclick={onclose}>Закрыть вкладку</Button>
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
    background: var(--bg);
  }
  .card {
    width: 560px;
    max-width: 100%;
    padding: 22px 24px 18px;
    background: #1e2228;
    border: 1px solid #333a44;
    border-radius: var(--radius-dialog);
    box-shadow: 0 16px 48px rgba(0, 0, 0, 0.45);
  }
  .head {
    display: flex;
    gap: 14px;
    margin-bottom: 16px;
  }
  .tile {
    display: grid;
    flex: none;
    place-items: center;
    width: 36px;
    height: 36px;
    border-radius: var(--radius-card);
    background: rgba(79, 143, 247, 0.14);
    color: var(--accent);
  }
  h2 {
    margin: 0 0 6px;
    font-size: 16px;
    font-weight: 600;
  }
  p {
    margin: 0;
    color: var(--muted);
    font-size: 13px;
    line-height: 1.5;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 14px;
  }
</style>
