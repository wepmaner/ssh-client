import { fireEvent } from '@testing-library/svelte'

function shown(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>('[role="option"]')]
}

/** Opens the list of a Select and presses the option with this value. */
export async function pick(field: HTMLElement, value: string): Promise<void> {
  await fireEvent.click(field)
  const option = shown().find((o) => o.dataset.value === value)
  if (!option) throw new Error(`no option ${value}`)
  await fireEvent.click(option)
}

async function read(field: HTMLElement, of: (o: HTMLElement) => string): Promise<string[]> {
  await fireEvent.click(field)
  const all = shown().map(of)
  await fireEvent.click(field)
  return all
}

export const optionLabels = (field: HTMLElement) => read(field, (o) => o.textContent?.trim() ?? '')
export const optionValues = (field: HTMLElement) => read(field, (o) => o.dataset.value ?? '')
