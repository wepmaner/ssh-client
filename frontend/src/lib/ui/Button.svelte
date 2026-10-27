<script lang="ts">
  import type { Snippet } from 'svelte'
  import type { HTMLButtonAttributes } from 'svelte/elements'

  interface Props extends HTMLButtonAttributes {
    variant?: 'primary' | 'outline' | 'ghost'
    children: Snippet
  }
  let { variant = 'outline', type = 'button', children, ...rest }: Props = $props()
</script>

<button {type} class="btn {variant}" {...rest}>{@render children()}</button>

<style>
  /* A fixed-height button never shrinks or wraps its label: a wrapped second line would spill out. */
  .btn {
    flex-shrink: 0;
    height: var(--control-height);
    padding: 0 14px;
    white-space: nowrap;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    font: inherit;
    cursor: default;
    transition: background-color 80ms;
  }
  .btn:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  .btn:disabled {
    opacity: 0.45;
  }
  .primary {
    background: var(--accent);
    color: #0f1114;
    font-weight: 600;
  }
  .primary:hover:not(:disabled) {
    background: #6aa0f9;
  }
  .outline {
    background: transparent;
    border-color: #333a44;
    color: var(--text);
  }
  .outline:hover:not(:disabled) {
    background: #262b33;
  }
  .ghost {
    background: transparent;
    color: var(--muted);
  }
  .ghost:hover:not(:disabled) {
    background: #262b33;
    color: var(--text);
  }
</style>
