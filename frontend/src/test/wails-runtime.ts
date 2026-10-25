// Stand-in for @wailsio/runtime under Vitest (aliased in vite.config.ts). The real module
// talks to the Go side on import; tests mock ../api, and the generated models only need Create.

export class CancellablePromise<T> extends Promise<T> {}

const noWails = () => Promise.reject(new Error('no Wails runtime in tests'))

export const Call = { ByID: noWails, ByName: noWails }

type Listener = (ev: { name: string; data: unknown }) => void
const listeners = new Map<string, Set<Listener>>()

export const Events = {
  On(name: string, cb: Listener): () => void {
    const set = listeners.get(name) ?? new Set<Listener>()
    set.add(cb)
    listeners.set(name, set)
    return () => set.delete(cb)
  },
  /** Test helper: delivers an event as the runtime would, wrapped in a WailsEvent. */
  Emit(name: string, data: unknown): void {
    for (const cb of listeners.get(name) ?? []) cb({ name, data })
  },
}

export const Clipboard = { SetText: (_text: string) => Promise.resolve(), Text: () => Promise.resolve('') }

export const Window = { Minimise: () => Promise.resolve(), ToggleMaximise: () => Promise.resolve(), Close: () => Promise.resolve() }

// Create mirrors @wailsio/runtime/src/create.ts for the generated models.
const Any = <T>(source: T): T => source
export const Create = {
  Any,
  Array:
    (element: (v: any) => any) =>
    (source: any[] | null): any[] => {
      if (source === null) return []
      if (element === Any) return source
      for (let i = 0; i < source.length; i++) source[i] = element(source[i])
      return source
    },
  Map:
    (_key: unknown, value: (v: any) => any) =>
    (source: Record<string, any> | null): Record<string, any> => {
      if (source === null) return {}
      if (value === Any) return source
      for (const k in source) source[k] = value(source[k])
      return source
    },
  Nullable:
    (element: (v: any) => any) =>
    (source: any): any =>
      source === null ? null : element(source),
  Events: {} as Record<string, unknown>,
}
