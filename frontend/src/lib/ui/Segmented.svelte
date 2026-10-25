<script lang="ts">
  interface Props {
    options: { value: string; label: string }[]
    value: string
    onchange: (value: string) => void
    label?: string
  }
  let { options, value, onchange, label = '' }: Props = $props()
</script>

<div class="seg" role="radiogroup" aria-label={label}>
  {#each options as o (o.value)}
    <button type="button" role="radio" aria-checked={o.value === value} class:on={o.value === value} onclick={() => onchange(o.value)}>
      {o.label}
    </button>
  {/each}
</div>

<style>
  .seg {
    display: inline-flex;
    gap: 2px;
    padding: 2px;
    background: #15181c;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
  }
  button {
    height: 28px;
    padding: 0 14px;
    border: 0;
    border-radius: 3px;
    background: transparent;
    color: var(--muted);
    font: inherit;
    cursor: default;
    transition: background-color 80ms;
  }
  button:hover {
    color: var(--text);
  }
  button.on {
    background: #2e333b;
    color: var(--text);
  }
  button:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
</style>
