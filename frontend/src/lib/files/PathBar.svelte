<script lang="ts">
  import { tick } from 'svelte'
  import { MOVE_TYPE, type FilesTab } from './files.svelte'
  import { crumbs } from './format'
  import Icon from './Icon.svelte'

  /**
   * The breadcrumbs of the folder; a click on the empty part of the bar or Ctrl+L turns it into
   * a path field. Rows dragged inside the list can be dropped on a crumb to move them there.
   */
  interface Props {
    model: FilesTab
    editing?: boolean
    /** The field closed with Enter or Esc: focus goes back to the list. */
    ondone?: () => void
  }
  let { model, editing = $bindable(false), ondone }: Props = $props()

  let value = $state('')
  let input: HTMLInputElement | undefined = $state()
  let strip: HTMLDivElement | undefined = $state()
  let over = $state('')
  const list = $derived(crumbs(model.path))

  /** Opens the field with the current path selected. */
  export async function edit(): Promise<void> {
    value = model.path
    editing = true
    await tick()
    input?.focus()
    input?.select()
  }

  function onkeydown(e: KeyboardEvent): void {
    if (e.key === 'Enter') {
      e.preventDefault()
      const target = value.trim()
      editing = false
      if (target) void model.navigate(target)
      ondone?.()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      editing = false
      ondone?.()
    }
  }

  // The last crumb stays in view when the path is longer than the bar.
  $effect(() => {
    void list
    if (strip) strip.scrollLeft = strip.scrollWidth
  })

  function ondragover(e: DragEvent, path: string): void {
    if (!e.dataTransfer?.types.includes(MOVE_TYPE) || model.dragging.length === 0) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'move'
    over = path
  }

  function ondrop(e: DragEvent, path: string): void {
    if (!e.dataTransfer?.types.includes(MOVE_TYPE)) return
    e.preventDefault()
    over = ''
    const paths = model.dragging
    model.dragging = []
    void model.move(paths, path)
  }
</script>

{#if editing}
  <label class="bar field">
    <input
      bind:this={input}
      bind:value
      type="text"
      aria-label="Путь на сервере"
      autocomplete="off"
      spellcheck="false"
      {onkeydown}
      onblur={() => (editing = false)}
    />
  </label>
{:else}
  <div class="bar" role="presentation" onclick={(e) => e.target === e.currentTarget && edit()}>
    <div class="crumbs" bind:this={strip} role="presentation" onclick={(e) => e.target === e.currentTarget && edit()}>
      <nav aria-label="Путь">
        {#each list as c, i (c.path)}
          {#if i > 1}<span class="sep"><Icon name="chevron-right" size={12} /></span>{/if}
          <button
            type="button"
            class="crumb"
            class:over={over === c.path}
            aria-current={i === list.length - 1 ? 'location' : undefined}
            title={c.path}
            onclick={() => model.navigate(c.path)}
            ondragover={(e) => ondragover(e, c.path)}
            ondragleave={() => over === c.path && (over = '')}
            ondrop={(e) => ondrop(e, c.path)}
          >
            {c.name}
          </button>
        {/each}
      </nav>
    </div>
  </div>
{/if}

<style>
  .bar {
    display: flex;
    flex: 1;
    align-items: center;
    min-width: 0;
    height: var(--control-height);
    padding: 0 4px;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
    cursor: text;
  }
  .field {
    padding: 0 10px;
  }
  .field:focus-within {
    border-color: var(--accent);
  }
  .field input {
    flex: 1;
    min-width: 0;
    border: 0;
    background: transparent;
    color: var(--text);
    font-family: var(--font-mono);
    font-size: 12.5px;
    outline: none;
    user-select: text;
  }
  .crumbs {
    flex: 1;
    min-width: 0;
    height: 100%;
    overflow-x: auto;
    scrollbar-width: none;
  }
  nav {
    display: flex;
    align-items: center;
    width: max-content;
    height: 100%;
  }
  .sep {
    display: grid;
    place-items: center;
    color: #6f7a87;
  }
  .crumb {
    height: 24px;
    padding: 0 6px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: 13px;
    white-space: nowrap;
    cursor: default;
    transition: background-color 80ms;
  }
  .crumb:hover {
    background: #262b33;
    color: var(--text);
  }
  .crumb[aria-current='location'] {
    color: var(--text);
  }
  .crumb.over {
    background: rgba(79, 143, 247, 0.18);
    color: var(--text);
    box-shadow: inset 0 0 0 1px var(--accent);
  }
  .crumb:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
</style>
