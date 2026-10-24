<script lang="ts">
  import { activeSession, sessions } from './sessions.svelte'
  import { hosts } from './stores.svelte'

  const tab = $derived(activeSession())
  const host = $derived(tab ? hosts.find((h) => h.id === tab.hostId) : undefined)
  const address = $derived(host ? `${host.user}@${host.address}:${host.port}` : '')

  /** «N сессий» with Russian plural forms. */
  function sessionsLabel(n: number): string {
    const m10 = n % 10
    const m100 = n % 100
    if (m10 === 1 && m100 !== 11) return `${n} сессия`
    if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return `${n} сессии`
    return `${n} сессий`
  }
</script>

<footer class="status">
  {#if tab}
    {#if tab.state === 'connected'}
      <span class="item"><span class="dot ok"></span>Подключено{tab.latencyMs ? ` · ${tab.latencyMs} мс` : ''}</span>
    {:else if tab.state === 'connecting'}
      <span class="item"><span class="dot"></span>Подключение…</span>
    {:else if tab.state === 'error'}
      <span class="item error"><span class="dot err"></span>Ошибка подключения</span>
    {:else}
      <span class="item"><span class="dot"></span>Сессия завершена</span>
    {/if}
    {#if address}<span class="item mono">{address}</span>{/if}
    {#if host?.key_name}
      <span class="item">
        <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M6 10a3 3 0 1 1 1.2-2.4L13.5 2.5M11 5l1.5 1.5M9.5 6.5 11 8" />
        </svg>
        {host.key_name}
      </span>
    {/if}
    <span class="spacer"></span>
    <span class="item">UTF-8</span>
    <span class="item mono">{tab.cols}×{tab.rows}</span>
  {:else}
    <span class="item">{sessionsLabel(sessions.list.length)}</span>
    <span class="spacer"></span>
    <span class="item">Хосты и ключи хранятся только на этом компьютере</span>
  {/if}
</footer>

<style>
  .status {
    display: flex;
    align-items: center;
    gap: 16px;
    height: 24px;
    flex: none;
    padding: 0 12px;
    background: var(--titlebar);
    border-top: 1px solid var(--border);
    color: var(--muted);
    font-size: 12px;
    white-space: nowrap;
    overflow: hidden;
  }
  .item {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .mono {
    font-family: var(--font-mono);
    font-size: 11px;
  }
  .spacer {
    flex: 1;
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: #6f7a87;
  }
  .ok {
    background: var(--ok);
  }
  .err {
    background: var(--error);
  }
  .error {
    color: var(--error);
  }
</style>
