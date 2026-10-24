/** Russian plural form of «хост» for a count. */
export function hostsLabel(n: number): string {
  const mod100 = n % 100
  const mod10 = n % 10
  if (mod100 >= 11 && mod100 <= 14) return `${n} хостов`
  if (mod10 === 1) return `${n} хост`
  if (mod10 >= 2 && mod10 <= 4) return `${n} хоста`
  return `${n} хостов`
}

export function typeLabel(type: string): string {
  if (type === 'ed25519') return 'Ed25519'
  if (type === 'rsa') return 'RSA'
  return type
}

export function typeTile(type: string): string {
  return type === 'ed25519' ? 'ed' : type
}

export function formatDate(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString('ru-RU')
}

export const CANCELLED = 'cancelled'
export const PASSPHRASE_PREFIX = 'passphrase_required:'
