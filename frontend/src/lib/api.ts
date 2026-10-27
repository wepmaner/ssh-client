// Thin typed layer over the generated Wails v3 bindings and runtime: the one import point
// for components, so tests mock a single module.
import { Clipboard, Events, Window } from '@wailsio/runtime'

export {
  ForwardAttach,
  ForwardDelete,
  ForwardHostAttach,
  ForwardHostWrite,
  ForwardHostsList,
  ForwardSave,
  ForwardStart,
  ForwardStartAll,
  ForwardStop,
  ForwardStopAll,
  ForwardsList,
  GetSettings,
  GroupDelete,
  GroupRename,
  GroupSave,
  GroupsList,
  HostDelete,
  HostSave,
  HostsExport,
  HostsList,
  ImportPuTTY,
  ImportSSHConfig,
  KeyCopyPublic,
  KeyDelete,
  KeyExport,
  KeyGenerate,
  KeyImportFile,
  KeyImportFileWithPassphrase,
  KeyImportText,
  KeyImportTextWithPassphrase,
  KeyRename,
  KeysList,
  OpenDataFolder,
  SaveSettings,
  SessionClose,
  SessionList,
  SessionOpen,
  SessionResize,
  SessionWrite,
  TrustAnswer,
  VaultCounts,
  VaultLock,
  VaultRemovePassword,
  VaultResetCorrupt,
  VaultSetPassword,
  VaultState,
  VaultUnlock,
  Version,
} from '../../bindings/sshclient/app/app'
export * as app from '../../bindings/sshclient/app/models'
export * as model from '../../bindings/sshclient/internal/model/models'

/**
 * Subscribes to a Go event; the callback gets the payload (WailsEvent.data), as Wails v2
 * EventsOn did. Returns the unsubscribe function.
 */
export function EventsOn<T = any>(name: string, cb: (data: T) => void): () => void {
  return Events.On(name, (ev) => cb(ev.data as T))
}

export const ClipboardSetText = (text: string): Promise<void> => Clipboard.SetText(text)
export const ClipboardGetText = (): Promise<string> => Clipboard.Text()

export const WindowMinimise = (): Promise<void> => Window.Minimise()
export const WindowToggleMaximise = (): Promise<void> => Window.ToggleMaximise()
/** Closes the window like its system close button: Go saves the geometry, then the app quits. */
export const WindowClose = (): Promise<void> => Window.Close()

/** Wails rejects with the Go error text; normalise to a string. */
export function errorText(e: unknown): string {
  if (typeof e === 'string') return e
  if (e instanceof Error) return e.message
  return String(e)
}
