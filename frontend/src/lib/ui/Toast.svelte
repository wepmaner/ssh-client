<script lang="ts">
  import { toastState } from '../stores.svelte'
</script>

{#if toastState.message}
  <div class="toast" role="status">{toastState.message}</div>
{/if}

<style>
  .toast {
    position: fixed;
    bottom: 24px;
    left: 50%;
    transform: translateX(-50%);
    padding: 8px 16px;
    background: #2e333b;
    border: 1px solid #333a44;
    border-radius: var(--radius-control);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
    font-size: 13px;
    z-index: 20;
  }
</style>
