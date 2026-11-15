<script module lang="ts">
  export type IconName =
    | 'back'
    | 'forward'
    | 'up'
    | 'refresh'
    | 'upload'
    | 'download'
    | 'newfolder'
    | 'eye'
    | 'search'
    | 'chevron'
    | 'chevron-right'
    | 'folder'
    | 'file'
    | 'link'
    | 'trash'
    | 'warning'
    | 'close'
    | 'check'
    | 'error'
    | 'cancelled'
    | 'copy'
</script>

<script lang="ts">
  /** Stroke icons of the file manager, drawn on a 16 px grid like the rest of the app. */
  let { name, size = 16 }: { name: IconName; size?: number } = $props()
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 16 16"
  fill="none"
  stroke="currentColor"
  stroke-width="1.4"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
>
  {#if name === 'copy'}
    <rect x="5.5" y="5.5" width="8" height="8" rx="1" /><path d="M10.5 5.5V3.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2" />
  {:else if name === 'back'}
    <path d="M13 8H3.5M7.5 4 3.5 8l4 4" />
  {:else if name === 'forward'}
    <path d="M3 8h9.5M8.5 4l4 4-4 4" />
  {:else if name === 'up'}
    <path d="M8 13V3.5M4 7.5l4-4 4 4" />
  {:else if name === 'refresh'}
    <path d="M13 8a5 5 0 1 1-1.6-3.7" /><path d="M13 2.5v3h-3" />
  {:else if name === 'upload'}
    <path d="M8 10.5V2.5M5 5.5l3-3 3 3" /><path d="M2.5 10v2.5a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V10" />
  {:else if name === 'download'}
    <path d="M8 2.5v8M5 7.5l3 3 3-3" /><path d="M2.5 10v2.5a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V10" />
  {:else if name === 'newfolder'}
    <path d="M14.25 8V5.75a1 1 0 0 0-1-1h-5.9l-1.5-1.5h-3.1a1 1 0 0 0-1 1v7.5a1 1 0 0 0 1 1H8" />
    <path d="M12 10v4M10 12h4" />
  {:else if name === 'eye'}
    <path d="M1.5 8S4 3.5 8 3.5 14.5 8 14.5 8 12 12.5 8 12.5 1.5 8 1.5 8z" /><circle cx="8" cy="8" r="2" />
  {:else if name === 'search'}
    <circle cx="7" cy="7" r="4.5" /><path d="m10.5 10.5 3 3" />
  {:else if name === 'chevron'}
    <path d="m4.5 6.5 3.5 3.5 3.5-3.5" />
  {:else if name === 'chevron-right'}
    <path d="m6.5 4.5 3.5 3.5-3.5 3.5" />
  {:else if name === 'folder'}
    <path d="M1.75 4.25a1 1 0 0 1 1-1h3.1l1.5 1.5h5.9a1 1 0 0 1 1 1v6.5a1 1 0 0 1-1 1H2.75a1 1 0 0 1-1-1z" />
  {:else if name === 'file'}
    <path d="M9 1.75H4.25a1 1 0 0 0-1 1v10.5a1 1 0 0 0 1 1h7.5a1 1 0 0 0 1-1V5.5z" /><path d="M9 1.75V5.5h3.75" />
  {:else if name === 'link'}
    <path d="M6.5 9.5 13 3M9.5 3H13v3.5" /><path d="M11.5 9.5v3a1 1 0 0 1-1 1h-7a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1h3" />
  {:else if name === 'trash'}
    <path d="M2.5 4h11M6.5 4V2.5h3V4M4 4l.7 9a1 1 0 0 0 1 .9h4.6a1 1 0 0 0 1-.9L12 4" />
  {:else if name === 'warning'}
    <path d="M8 1.8 14.6 13.5H1.4z" /><path d="M8 6.2v3.4M8 11.6h.01" />
  {:else if name === 'close'}
    <path d="m4.5 4.5 7 7M11.5 4.5l-7 7" />
  {:else if name === 'check'}
    <path d="m3.5 8.5 3 3 6-6.5" />
  {:else if name === 'error'}
    <circle cx="8" cy="8" r="6" /><path d="m6 6 4 4M10 6l-4 4" />
  {:else if name === 'cancelled'}
    <circle cx="8" cy="8" r="6" /><path d="M5.5 10.5l5-5" />
  {/if}
</svg>
