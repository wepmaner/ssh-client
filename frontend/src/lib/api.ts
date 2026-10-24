// Thin typed layer over the generated Wails bindings.
export {
  GetSettings,
  HostsListTemp,
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
  SaveSettings,
  SessionClose,
  SessionList,
  SessionOpen,
  SessionResize,
  SessionWrite,
  VaultCounts,
  VaultRemovePassword,
  VaultResetCorrupt,
  VaultSetPassword,
  VaultState,
  VaultUnlock,
} from '../../wailsjs/go/app/App'
export { app, model } from '../../wailsjs/go/models'
export { EventsOn } from '../../wailsjs/runtime/runtime'

/** Wails rejects with the Go error text; normalise to a string. */
export function errorText(e: unknown): string {
  if (typeof e === 'string') return e
  if (e instanceof Error) return e.message
  return String(e)
}
