<script lang="ts">
  import { FilesShowLocal, errorText } from '../api'
  import { setToastLift, toast } from '../stores.svelte'
  import { isActive, isFinished, type FilesTab, type Job } from './files.svelte'
  import Icon, { type IconName } from './Icon.svelte'
  import { jobFraction, jobStatus, orderJobs, queueSummary } from './jobs'

  /**
   * The queue at the bottom of a file tab. Its head shows the summary while something runs and only
   * a muted title once everything has finished; the rows run first, finished ones follow, newest first.
   */
  let { model }: { model: FilesTab } = $props()

  let expanded = $state(true)
  let panel: HTMLElement | undefined = $state()

  // The toast stays above the panel while the panel is on screen; a hidden tab's panel measures 0.
  $effect(() => {
    const el = panel
    const owner = 'transfers:' + model.id
    if (!el) return
    const measure = () => {
      const r = el.getBoundingClientRect()
      setToastLift(owner, r.height > 0 ? window.innerHeight - r.top : 0)
    }
    measure()
    const ro = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure)
    ro?.observe(el)
    window.addEventListener('resize', measure)
    return () => {
      ro?.disconnect()
      window.removeEventListener('resize', measure)
      setToastLift(owner, 0)
    }
  })
  let details = $state<string[]>([])
  let now = $state(Date.now())

  // The countdown of a waiting job ticks locally; Go sends it once per pause.
  $effect(() => {
    if (!model.jobs.some((j) => j.state === 'waiting')) return
    now = Date.now()
    const t = setInterval(() => (now = Date.now()), 1000)
    return () => clearInterval(t)
  })

  const summary = $derived(queueSummary(model.jobs, now))
  const rows = $derived(orderJobs(model.jobs))
  const finished = $derived(model.jobs.filter(isFinished).length)

  const direction = (j: Job) =>
    j.kind === 'upload' ? 'Загрузка на сервер' : j.kind === 'download' ? 'Скачивание' : j.kind === 'copy' ? 'Копирование на сервере' : 'Удаление'
  const activeIcon: Record<Job['kind'], IconName> = { upload: 'upload', download: 'download', copy: 'copy', delete: 'trash' }

  function stateIcon(j: Job): { name: IconName; label: string } {
    if (isActive(j)) return { name: activeIcon[j.kind] ?? 'file', label: direction(j) }
    if (j.state === 'failed') return { name: 'error', label: 'Ошибка' }
    if (j.state === 'cancelled') return { name: 'cancelled', label: 'Отменено' }
    if (j.failed > 0) return { name: 'warning', label: 'Готово, с ошибками' }
    return { name: 'check', label: 'Готово' }
  }

  const retryable = (j: Job) => j.state === 'failed' || j.state === 'cancelled' || (j.state === 'done' && j.failed > 0)

  async function reveal(j: Job): Promise<void> {
    try {
      await FilesShowLocal(j.dest)
    } catch (e) {
      toast(errorText(e))
    }
  }

  function toggleDetail(id: string): void {
    details = details.includes(id) ? details.filter((d) => d !== id) : [...details, id]
  }
</script>

<section class="transfers" aria-label="Передачи" bind:this={panel}>
  <div class="head">
    <button type="button" class="toggle" aria-expanded={expanded} onclick={() => (expanded = !expanded)}>
      <span class="chev" class:open={expanded}><Icon name="chevron" size={12} /></span>
      {#if summary}
        <span class="summary">{summary}</span>
      {:else}
        <span class="caption">Передачи</span>
      {/if}
    </button>
    {#if finished > 0}
      <button type="button" class="text-btn" title="Убрать завершённые из списка" onclick={() => model.clearFinished()}>Очистить</button>
    {/if}
  </div>
  {#if expanded}
    <ul class="jobs">
      {#each rows as j (j.id)}
        {@const ico = stateIcon(j)}
        {@const active = isActive(j)}
        <li class="job {j.state}" class:finished={!active} class:partial={j.state === 'done' && j.failed > 0}>
          <span class="ico" title={ico.label}><Icon name={ico.name} size={14} /></span>
          <span class="title" title={j.title}>
            {j.title}{#if j.state === 'running' && j.current}<span class="current"> · {j.current}</span>{/if}
          </span>
          <span class="status">{jobStatus(j, now)}</span>
          <span class="actions">
            {#if j.state === 'waiting'}
              <button type="button" class="text-btn" onclick={() => model.retryJob(j.id)}>Повторить сейчас</button>
            {/if}
            {#if !active && j.detail}
              <button type="button" class="text-btn" aria-expanded={details.includes(j.id)} onclick={() => toggleDetail(j.id)}>
                {details.includes(j.id) ? 'Скрыть' : 'Подробности'}
              </button>
            {/if}
            {#if retryable(j)}
              <button type="button" class="text-btn" onclick={() => model.retryJob(j.id)}>Повторить</button>
            {/if}
            {#if j.kind === 'download' && j.state === 'done' && j.dest}
              <button type="button" class="icon-btn" aria-label="Показать в папке" title="Показать в папке" onclick={() => reveal(j)}>
                <Icon name="folder" size={14} />
              </button>
            {/if}
            {#if active}
              <button type="button" class="icon-btn" aria-label="Отменить" title="Отменить" onclick={() => model.cancelJob(j.id)}>
                <Icon name="close" size={14} />
              </button>
            {:else}
              <button type="button" class="icon-btn remove" aria-label="Убрать из списка" title="Убрать из списка" onclick={() => model.removeJob(j.id)}>
                <Icon name="close" size={14} />
              </button>
            {/if}
          </span>
          {#if active && (j.kind !== 'delete' || j.state === 'running')}
            <div
              class="bar"
              role="progressbar"
              aria-label={j.title}
              aria-valuemin="0"
              aria-valuemax="100"
              aria-valuenow={Math.floor(jobFraction(j) * 100)}
            >
              <span style:width="{jobFraction(j) * 100}%"></span>
            </div>
          {/if}
          {#if details.includes(j.id) && j.detail}<pre>{j.detail}</pre>{/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .transfers {
    flex: none;
    border-top: 1px solid var(--border);
    background: var(--sidebar);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 4px;
    height: 32px;
    padding: 0 8px 0 4px;
  }
  .toggle {
    display: flex;
    flex: 1;
    align-items: center;
    gap: 6px;
    min-width: 0;
    height: 24px;
    padding: 0 6px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 12px;
    text-align: left;
    cursor: default;
    transition: background-color 80ms;
  }
  .toggle:hover {
    background: #22262d;
  }
  .toggle:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .chev {
    display: grid;
    color: var(--muted);
    transform: rotate(-90deg);
  }
  .chev.open {
    transform: none;
  }
  .summary {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  .caption {
    color: var(--muted);
  }
  .jobs {
    max-height: 168px;
    margin: 0;
    padding: 0 8px 6px;
    overflow: auto;
    list-style: none;
  }
  .job {
    position: relative;
    display: grid;
    grid-template-columns: 16px minmax(0, 1fr) auto auto;
    align-items: center;
    column-gap: 10px;
    min-height: 32px;
    padding: 0 4px 0 8px;
    border-radius: var(--radius-control);
    font-size: 12.5px;
  }
  .job:hover {
    background: #1c1f24;
  }
  .ico {
    display: grid;
    place-items: center;
    color: var(--muted);
  }
  .done .ico {
    color: var(--ok);
  }
  .partial .ico,
  .waiting .ico {
    color: #e3b341;
  }
  .failed .ico {
    color: var(--error);
  }
  .title {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .finished .title {
    color: #c9ced6;
  }
  .current {
    color: #7d8794;
  }
  .status {
    color: var(--muted);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .failed .status {
    max-width: 260px;
    overflow: hidden;
    text-overflow: ellipsis;
    color: var(--error);
  }
  .waiting .status {
    color: #e3b341;
  }
  .actions {
    display: flex;
    align-items: center;
    gap: 2px;
  }
  .text-btn {
    height: 24px;
    padding: 0 8px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--accent);
    font: inherit;
    font-size: 12px;
    white-space: nowrap;
    cursor: default;
    transition: background-color 80ms;
  }
  .icon-btn {
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
  .text-btn:hover,
  .icon-btn:hover {
    background: #262b33;
  }
  .icon-btn:hover {
    color: var(--text);
  }
  .text-btn:focus-visible,
  .icon-btn:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  /* A finished row's × appears on hover and keyboard focus; an active row's × (cancel) always shows. */
  .remove {
    opacity: 0;
  }
  .job:hover .remove,
  .job:focus-within .remove {
    opacity: 1;
  }
  .bar {
    position: absolute;
    right: 8px;
    bottom: 3px;
    left: 34px;
    height: 2px;
    overflow: hidden;
    border-radius: 1px;
    background: #2a2f37;
  }
  .bar span {
    display: block;
    height: 100%;
    background: var(--accent);
  }
  .waiting .bar span {
    background: #6f7a87;
  }
  pre {
    grid-column: 2 / -1;
    max-height: 120px;
    margin: 0 0 6px;
    padding: 6px 8px;
    overflow: auto;
    background: var(--terminal);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11.5px;
    white-space: pre-wrap;
    word-break: break-word;
    user-select: text;
  }
</style>
