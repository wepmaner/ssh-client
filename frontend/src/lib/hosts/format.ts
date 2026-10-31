import type { app } from '../api'

/** The modes of a host, in the host dialog and in the MCP section. */
export const mcpAccessOptions = [
  { value: '', label: 'Закрыт' },
  { value: 'read', label: 'Только просмотр' },
  { value: 'full', label: 'Полный' },
]

/** Monogram of a host tile: the OS picks the colour, other hosts show the first two letters of the name. */
export function monogram(h: Pick<app.HostDTO, 'os' | 'name'>): { text: string; color: string } {
  if (h.os === 'ubuntu') return { text: 'Ub', color: '#b9521c' }
  if (h.os === 'debian') return { text: 'Db', color: '#b8405a' }
  const s = h.name.trim().slice(0, 2)
  return { text: s.charAt(0).toUpperCase() + s.slice(1).toLowerCase(), color: '#1f8a80' }
}

/** user@address, with the port only when it is not 22. */
export function userAtHost(h: Pick<app.HostDTO, 'user' | 'address' | 'port'>): string {
  return `${h.user}@${h.address}${h.port && h.port !== 22 ? ':' + h.port : ''}`
}

/** «2 подключены», for the group tiles. */
export function connectedLabel(n: number): string {
  return `${n} ${n % 10 === 1 && n % 100 !== 11 ? 'подключён' : 'подключены'}`
}

/** Splits a Go validation error "field:<name>: text"; other errors give null. */
export function fieldError(msg: string): { field: string; text: string } | null {
  const m = /^field:(\w+): (.*)$/s.exec(msg)
  return m ? { field: m[1], text: m[2] } : null
}
