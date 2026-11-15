<script lang="ts">
  import { FilesShowLocal, errorText } from '../api'
  import { toast } from '../stores.svelte'
  import Button from '../ui/Button.svelte'
  import { isActive, isFinished, type FilesTab, type Job } from './files.svelte'
  import Icon from './Icon.svelte'
  import { jobFraction, jobStatus, queueSummary } from './jobs'

  /** The queue at the bottom of a file tab: a summary row that opens the list of jobs. */
  let { model }: { model: FilesTab } = $props()

  let expanded = $state(true)
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
  const finished = $derived(model.jobs.filter(isFinished).length)
  const running = $derived(model.jobs.some(isActive))

  const icon = (j: Job) => (j.kind === 'upload' ? 'upload' : j.kind === 'download' ? 'download' : 'trash')
  const direction = (j: Job) => (j.kind === 'upload' ? 'Загрузка на сервер' : j.kind === 'download' ? 'Скачивание' : 'Удаление')

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

<section class="transfers" aria-label="Передачи">
  <div class="summary">
    <button type="button" class="toggle" aria-expanded={expanded} onclick={() => (expanded = !expanded)}>
      <span class="chev" class:open={expanded}><Icon name="chevron" size={12} /></span>
      <span class="summary-text" class:live={running}>{summary}</span>
    </button>
    {#if finished > 0}
      <Button variant="ghost" onclick={() => model.clearFinished()} style="height:26px;padding:0 10px;font-size:12px">Очистить завершённые</Button>
    {/if}
  </div>
  {#if expanded}
    <ul class="jobs">
      {#each model.jobs as j (j.id)}
        <li class="job {j.state}">
          <span class="dir" title={direction(j)}><Icon name={icon(j)} size={14} /></span>
          <div class="main">
            <div class="line">
              <span class="title" title={j.title}>{j.title}</span>
              <span class="status">{jobStatus(j, now)}</span>
            </div>
            {#if j.kind !== 'delete' || j.state === 'running'}
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
            {#if j.state === 'running' && j.current}<span class="current" title={j.current}>{j.current}</span>{/if}
            {#if details.includes(j.id) && j.detail}<pre>{j.detail}</pre>{/if}
          </div>
          <div class="actions">
            {#if j.detail && isFinished(j)}
              <button type="button" class="act" onclick={() => toggleDetail(j.id)}>{details.includes(j.id) ? 'Скрыть' : 'Подробности'}</button>
            {/if}
            {#if isActive(j)}
              <button type="button" class="act" onclick={() => model.cancelJob(j.id)}>Отмена</button>
            {/if}
            {#if j.state === 'failed' || j.state === 'cancelled'}
              <button type="button" class="act" onclick={() => model.retryJob(j.id)}>Повторить</button>
            {/if}
            {#if j.kind === 'download' && j.state === 'done' && j.dest}
              <button type="button" class="act" onclick={() => reveal(j)}>Показать в папке</button>
            {/if}
            {#if isFinished(j)}
              <button type="button" class="act" onclick={() => model.removeJob(j.id)}>Убрать</button>
            {/if}
          </div>
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
  .summary {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 34px;
    padding: 0 10px 0 6px;
  }
  .toggle {
    display: flex;
    flex: 1;
    align-items: center;
    gap: 6px;
    min-width: 0;
    height: 26px;
    padding: 0 8px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 12.5px;
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
  .summary-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }
  .summary-text.live {
    color: var(--text);
  }
  .jobs {
    max-height: 196px;
    margin: 0;
    padding: 0 8px 8px;
    overflow: auto;
    list-style: none;
  }
  .job {
    display: grid;
    grid-template-columns: 22px 1fr auto;
    align-items: start;
    gap: 8px;
    padding: 7px 8px;
    border-radius: var(--radius-control);
  }
  .job:hover {
    background: #1c1f24;
  }
  .dir {
    display: grid;
    place-items: center;
    height: 18px;
    color: var(--muted);
  }
  .main {
    display: flex;
    flex-direction: column;
    gap: 5px;
    min-width: 0;
  }
  .line {
    display: flex;
    align-items: baseline;
    gap: 12px;
    min-width: 0;
    font-size: 12.5px;
  }
  .title {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .status {
    flex: none;
    margin-left: auto;
    color: var(--muted);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }
  .failed .status {
    color: var(--error);
  }
  .waiting .status {
    color: #e3b341;
  }
  .done .status {
    color: var(--ok);
  }
  .bar {
    height: 3px;
    overflow: hidden;
    border-radius: 2px;
    background: #2a2f37;
  }
  .bar span {
    display: block;
    height: 100%;
    background: var(--accent);
  }
  .waiting .bar span,
  .cancelled .bar span {
    background: #6f7a87;
  }
  .failed .bar span {
    background: var(--error);
  }
  .done .bar span {
    background: var(--ok);
  }
  .current {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: #7d8794;
    font-family: var(--font-mono);
    font-size: 11px;
  }
  pre {
    max-height: 120px;
    margin: 2px 0 0;
    padding: 8px 10px;
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
  .actions {
    display: flex;
    gap: 2px;
  }
  .act {
    height: 22px;
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
  .act:hover {
    background: #262b33;
  }
  .act:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
</style>
