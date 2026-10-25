<script lang="ts">
  import type { ISearchOptions, SearchAddon } from '@xterm/addon-search'
  import { onMount } from 'svelte'

  let { search, onclose }: { search: SearchAddon; onclose: () => void } = $props()

  let query = $state('')
  let index = $state(-1)
  let count = $state(0)
  let input: HTMLInputElement

  const options: ISearchOptions = {
    decorations: {
      matchBackground: '#3a3f2a',
      matchOverviewRuler: '#e3b341',
      activeMatchBackground: '#6b5a1e',
      activeMatchBorder: '#e3b341',
      activeMatchColorOverviewRuler: '#e3b341',
    },
  }

  onMount(() => {
    input.focus()
    const sub = search.onDidChangeResults((r) => {
      index = r.resultIndex
      count = r.resultCount
    })
    return () => {
      sub.dispose()
      search.clearDecorations()
    }
  })

  function find(forward: boolean, incremental = false): void {
    if (!query) {
      search.clearDecorations()
      index = -1
      count = 0
      return
    }
    if (forward) search.findNext(query, { ...options, incremental })
    else search.findPrevious(query, options)
  }

  function onkeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault()
      onclose()
    } else if (e.key === 'Enter') {
      e.preventDefault()
      find(!e.shiftKey)
    }
  }
</script>

<div class="search" role="search">
  <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" aria-hidden="true">
    <circle cx="7" cy="7" r="4.5" />
    <path d="m10.5 10.5 3 3" />
  </svg>
  <input bind:this={input} bind:value={query} oninput={() => find(true, true)} {onkeydown} placeholder="Найти" aria-label="Найти в терминале" spellcheck="false" />
  <span class="count">{count > 0 && index >= 0 ? index + 1 : 0} из {count}</span>
  <button type="button" class="icon" aria-label="Предыдущее совпадение" onclick={() => find(false)}>
    <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="m4 10 4-4 4 4" />
    </svg>
  </button>
  <button type="button" class="icon" aria-label="Следующее совпадение" onclick={() => find(true)}>
    <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="m4 6 4 4 4-4" />
    </svg>
  </button>
  <button type="button" class="icon" aria-label="Закрыть поиск" onclick={onclose}>
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.2" aria-hidden="true">
      <path d="M1 1l8 8M9 1 1 9" />
    </svg>
  </button>
</div>

<style>
  .search {
    position: absolute;
    top: 10px;
    right: 18px;
    z-index: 5;
    display: flex;
    align-items: center;
    gap: 6px;
    height: 34px;
    padding: 0 6px 0 10px;
    background: #1e2228;
    border: 1px solid #333a44;
    border-radius: 6px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
    color: var(--muted);
  }
  /* The field inside has no border of its own: the panel shows its focus, as the hosts search does. */
  .search:has(input:focus) {
    border-color: var(--accent);
  }
  input {
    width: 200px;
    height: 24px;
    padding: 0 4px;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--text);
    font-family: var(--font-mono);
    font-size: 12px;
  }
  .count {
    min-width: 52px;
    font-size: 12px;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .icon {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    cursor: default;
    transition: background-color 80ms;
  }
  .icon:hover {
    background: #262b33;
    color: var(--text);
  }
  .icon:focus-visible {
    outline: 2px solid var(--focus);
  }
</style>
