<script module lang="ts">
  export interface SelectOption {
    value: string
    label: string
  }

  let seq = 0
</script>

<script lang="ts">
  interface Props {
    /** The caller owns the value: a refused change simply leaves it as it was. */
    value: string
    options: SelectOption[]
    onchange: (value: string) => void
    id?: string
    disabled?: boolean
    style?: string
    'aria-label'?: string
    'aria-describedby'?: string
  }
  let { value, options, onchange, id, disabled = false, style, 'aria-label': ariaLabel, 'aria-describedby': describedBy }: Props = $props()

  const GAP = 4
  const listId = `select-${++seq}`

  let trigger: HTMLButtonElement | undefined = $state()
  let list: HTMLDivElement | undefined = $state()
  let open = $state(false)
  let active = $state(0)
  let pos = $state<{ top: number; left: number; width: number } | null>(null)

  const current = $derived(options.find((o) => o.value === value))

  function show() {
    if (disabled || options.length === 0) return
    active = Math.max(0, options.findIndex((o) => o.value === value))
    open = true
  }

  function pick(o: SelectOption) {
    open = false
    if (o.value !== value) onchange(o.value)
  }

  // Below the field, or above it when there is no room; fixed, so no overflow of a parent clips it.
  $effect(() => {
    if (!open || !list || !trigger) return
    const r = trigger.getBoundingClientRect()
    const h = list.offsetHeight
    const w = Math.max(list.offsetWidth, r.width)
    const below = r.bottom + GAP
    const top = below + h > window.innerHeight && r.top - GAP - h >= 0 ? r.top - GAP - h : below
    pos = { top, left: Math.max(0, Math.min(r.left, window.innerWidth - w)), width: r.width }
    // A fixed list would drift away from a scrolled field: close instead.
    const onscroll = (e: Event) => {
      if (!list?.contains(e.target as Node)) open = false
    }
    document.addEventListener('scroll', onscroll, true)
    return () => document.removeEventListener('scroll', onscroll, true)
  })

  $effect(() => {
    if (open && list) list.children[active]?.scrollIntoView?.({ block: 'nearest' })
  })

  function move(to: number) {
    active = Math.max(0, Math.min(to, options.length - 1))
  }

  function onkeydown(e: KeyboardEvent) {
    if (!open) {
      if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(e.key)) {
        e.preventDefault()
        show()
      }
      return
    }
    switch (e.key) {
      case 'Escape':
        e.preventDefault()
        e.stopPropagation() // only the list closes, not the dialog around it
        open = false
        return
      case 'Tab':
        open = false
        return
      case 'Enter':
      case ' ':
        pick(options[active])
        break
      case 'ArrowDown':
        move(active + 1)
        break
      case 'ArrowUp':
        move(active - 1)
        break
      case 'Home':
        move(0)
        break
      case 'End':
        move(options.length - 1)
        break
      default:
        return
    }
    e.preventDefault()
  }
</script>

<svelte:window onresize={() => (open = false)} />

<button
  bind:this={trigger}
  {id}
  type="button"
  class="select"
  role="combobox"
  data-value={value}
  {style}
  {disabled}
  aria-label={ariaLabel}
  aria-describedby={describedBy}
  aria-expanded={open}
  aria-haspopup="listbox"
  aria-controls={open ? listId : undefined}
  aria-activedescendant={open ? `${listId}-${active}` : undefined}
  onclick={() => (open ? (open = false) : show())}
  {onkeydown}
  onblur={() => (open = false)}
>
  <span class="text">{current?.label ?? ''}</span>
  <svg class="caret" width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
    <path d="M2 3.5 5 6.5 8 3.5" fill="none" stroke="currentColor" stroke-width="1.3" />
  </svg>
</button>

{#if open}
  <!-- Focus stays on the field: a press here must not take it away. -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    bind:this={list}
    id={listId}
    class="list"
    role="listbox"
    tabindex="-1"
    style:top={pos ? pos.top + 'px' : undefined}
    style:left={pos ? pos.left + 'px' : undefined}
    style:min-width={pos ? pos.width + 'px' : undefined}
    style:visibility={pos ? undefined : 'hidden'}
    onmousedown={(e) => e.preventDefault()}
  >
    {#each options as o, i (o.value)}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        id={`${listId}-${i}`}
        class="option"
        class:active={i === active}
        class:selected={o.value === value}
        role="option"
        tabindex="-1"
        data-value={o.value}
        aria-selected={o.value === value}
        onclick={() => pick(o)}
        onmousemove={() => (active = i)}
      >
        {o.label}
      </div>
    {/each}
  </div>
{/if}

<style>
  .select {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    width: 100%;
    min-width: 0;
    height: var(--control-height);
    padding: 0 8px;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
    color: var(--text);
    font: inherit;
    text-align: left;
    cursor: default;
  }
  .select:focus {
    outline: none;
    border-color: var(--accent);
  }
  .select:disabled {
    opacity: 0.45;
  }
  .text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .caret {
    flex: none;
    color: var(--muted);
  }
  .list {
    position: fixed;
    z-index: 100;
    max-width: min(480px, 100vw);
    max-height: 280px;
    padding: 4px;
    overflow: auto;
    background: #1e2228;
    border: 1px solid #333a44;
    border-radius: var(--radius-card);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
  }
  .option {
    display: flex;
    align-items: center;
    min-height: 30px;
    padding: 0 10px;
    border-radius: var(--radius-control);
    color: var(--text);
    white-space: nowrap;
    cursor: default;
  }
  .option.active {
    background: #262b33;
  }
  .option.selected {
    color: var(--accent);
  }
</style>
