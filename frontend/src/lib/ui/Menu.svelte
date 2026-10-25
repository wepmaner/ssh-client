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
    /**
     * The button that opened the menu. The menu is placed at it with fixed positioning, so no
     * overflow of a parent clips it, and pressing the button again does not count as an outside click.
     */
    anchor?: HTMLElement
  }
  let { items, onclose, anchor }: Props = $props()

  const GAP = 4

  let el: HTMLDivElement | undefined = $state()
  let pos = $state<{ top: number; left: number } | null>(null)

  // Below the anchor with right edges aligned; flipped when there is no room.
  // Right-click opens the native menus instead (lib/menus.ts).
  function place() {
    if (!el) return
    const h = el.offsetHeight
    const w = el.offsetWidth
    if (!anchor) return
    const r = anchor.getBoundingClientRect()
    const below = r.bottom + GAP
    const top = below + h > window.innerHeight && r.top - GAP - h >= 0 ? r.top - GAP - h : below
    pos = { top, left: Math.max(0, r.right - w) }
  }

  $effect(() => {
    place()
    el?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
    // A fixed menu would drift away from a scrolled anchor: close instead.
    const onscroll = (e: Event) => !el?.contains(e.target as Node) && onclose()
    document.addEventListener('scroll', onscroll, true)
    return () => document.removeEventListener('scroll', onscroll, true)
  })

  /** Tab or a click elsewhere moves focus out: the menu closes, focus goes where it was sent. */
  function onfocusout(e: FocusEvent) {
    const to = e.relatedTarget as Node | null
    if (to && !el?.contains(to)) onclose()
  }

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

<svelte:window {onmousedown} onresize={onclose} />

<div
  class="menu"
  class:fixed={!!anchor}
  role="menu"
  tabindex="-1"
  bind:this={el}
  style:top={pos ? pos.top + 'px' : undefined}
  style:left={pos ? pos.left + 'px' : undefined}
  style:right={pos ? 'auto' : undefined}
  {onkeydown}
  {onfocusout}
>
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
  .menu.fixed {
    position: fixed;
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
