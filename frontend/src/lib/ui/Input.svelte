<script lang="ts">
  import type { HTMLInputAttributes } from 'svelte/elements'

  interface Props extends HTMLInputAttributes {
    value?: string
    el?: HTMLInputElement
  }
  let { value = $bindable(''), el = $bindable(), class: cls = '', ...rest }: Props = $props()
</script>

<input bind:this={el} bind:value class="input {cls}" {...rest} />

<style>
  .input {
    width: 100%;
    height: var(--control-height);
    padding: 0 10px;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    background: #15181c;
    color: var(--text);
    font: inherit;
    user-select: text;
  }
  .input:focus {
    outline: none;
    border-color: var(--accent);
  }
</style>
