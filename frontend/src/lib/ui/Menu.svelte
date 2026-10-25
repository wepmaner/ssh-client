<script module lang="ts">
  export interface MenuItem {
    label: string
    onselect: () => void
    danger?: boolean
  }
</script>

<script lang="ts">
  interface Props {
    items: MenuItem[]
    onclose: () => void
    /** The button that opened the menu: pressing it again must not reopen through the outside click. */
    anchor?: HTMLElement
  }
  let { items, onclose, anchor }: Props = $props()

  let el: HTMLDivElement | undefined = $state()

  $effect(() => {
    el?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
  })

  function onmousedown(e: MouseEvent) {
    const t = e.target as Node
    if (el?.contains(t) || anchor?.contains(t)) return
    onclose()
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      onclose()
      anchor?.focus()
      return
    }
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    e.preventDefault()
    const all = [...(el?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]
    const i = all.indexOf(document.activeElement as HTMLElement)
    all[(i + (e.key === 'ArrowDown' ? 1 : -1) + all.length) % all.length]?.focus()
  }

  function select(it: MenuItem) {
    onclose()
    it.onselect()
  }
</script>

<svelte:window {onmousedown} />

<div class="menu" role="menu" tabindex="-1" bind:this={el} {onkeydown}>
  {#each items as it (it.label)}
    <button type="button" role="menuitem" class:danger={it.danger} onclick={() => select(it)}>{it.label}</button>
  {/each}
</div>

<style>
  .menu {
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    z-index: 5;
    display: flex;
    flex-direction: column;
    min-width: 168px;
    padding: 4px;
    background: #1e2228;
    border: 1px solid #333a44;
    border-radius: var(--radius-card);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
  }
  button {
    height: 30px;
    padding: 0 10px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    font: inherit;
    text-align: left;
    cursor: default;
    transition: background-color 80ms;
  }
  button:hover,
  button:focus-visible {
    background: #262b33;
    outline: none;
  }
  .danger {
    color: var(--error);
  }
</style>
