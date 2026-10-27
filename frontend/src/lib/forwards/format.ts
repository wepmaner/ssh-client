/** A listen address as shown to the user: 127.0.0.1 is the default, so only the port is left. */
export function shortListen(addr: string): string {
  return addr.startsWith('127.0.0.1:') ? addr.slice('127.0.0.1:'.length) : addr
}
