// Keyboard shortcuts and clipboard access of the terminal, as in Windows Terminal.
import { ClipboardGetText, ClipboardSetText } from '../../../wailsjs/runtime/runtime'

export type TerminalAction = 'copy' | 'paste' | 'search'
export type TabAction = 'next' | 'prev' | 'close'

// Physical key codes keep the shortcuts working in the Russian layout.

/** Shortcuts the terminal view handles itself. */
export function terminalAction(e: KeyboardEvent): TerminalAction | null {
  if (e.type !== 'keydown' || !e.ctrlKey || e.altKey || e.metaKey) return null
  if (e.shiftKey && e.code === 'KeyC') return 'copy'
  if (e.shiftKey && e.code === 'KeyV') return 'paste'
  if (!e.shiftKey && e.code === 'KeyF') return 'search'
  return null
}

/** Window-level tab shortcuts: Ctrl+Tab, Ctrl+Shift+Tab, Ctrl+W. */
export function tabAction(e: KeyboardEvent): TabAction | null {
  if (e.type !== 'keydown' || !e.ctrlKey || e.altKey || e.metaKey) return null
  if (e.key === 'Tab') return e.shiftKey ? 'prev' : 'next'
  if (!e.shiftKey && e.code === 'KeyW') return 'close'
  return null
}

/** True when xterm must not see the key because the app handles it. */
export function isAppShortcut(e: KeyboardEvent): boolean {
  return terminalAction(e) !== null || tabAction(e) !== null
}

export async function copyText(text: string): Promise<void> {
  if (text) await ClipboardSetText(text)
}

export async function readClipboard(): Promise<string> {
  return (await ClipboardGetText()) ?? ''
}
