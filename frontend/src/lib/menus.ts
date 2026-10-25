// Native context menus. Go registers them by name (app/menus.go); an element opens one on
// right-click when it carries the CSS custom properties
//   style:--custom-contextmenu={name} style:--custom-contextmenu-data={id}
// (read by the Wails runtime, inherited by children). A click comes back as menu:action.
import { EventsOn } from './api'

export type MenuName = 'host' | 'group' | 'key' | 'forward'

/** Payload of menu:action, see app.MenuActionDTO. */
export interface MenuAction {
  menu: MenuName
  action: string
  id: string
}

export const MENU_ACTION_EVENT = 'menu:action'

/** Runs handlers[action](id) for clicks in the native menu `menu`. Returns the unsubscribe function. */
export function onMenuAction(menu: MenuName, handlers: Record<string, (id: string) => void>): () => void {
  return EventsOn(MENU_ACTION_EVENT, (a: MenuAction) => {
    if (a?.menu === menu && a.id) handlers[a.action]?.(a.id)
  })
}
