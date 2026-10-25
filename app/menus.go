package app

import (
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// menuActionEvent carries a click in a native context menu to the frontend.
const menuActionEvent = "menu:action"

// MenuActionDTO is the payload of menu:action: which menu, which item, and the
// id of the element it was opened on.
type MenuActionDTO struct {
	Menu   string `json:"menu"`
	Action string `json:"action"`
	ID     string `json:"id"`
}

type menuItem struct {
	action string
	label  string
	danger bool // set apart by a separator; a native menu has no red items
}

// contextMenus are the native right-click menus. The frontend opens one with
// the CSS custom properties --custom-contextmenu: <name> and
// --custom-contextmenu-data: <id> on the element.
var contextMenus = []struct {
	name  string
	items []menuItem
}{
	{"host", []menuItem{{"edit", "Изменить", false}, {"duplicate", "Дублировать", false}, {"delete", "Удалить", true}}},
	{"group", []menuItem{{"rename", "Переименовать", false}, {"delete", "Удалить", true}}},
	{"key", []menuItem{{"rename", "Переименовать", false}, {"delete", "Удалить", true}}},
	{"forward", []menuItem{{"edit", "Изменить", false}, {"delete", "Удалить", true}}},
}

// registerContextMenus builds the native menus once at startup.
func (a *App) registerContextMenus() {
	for _, m := range contextMenus {
		menu := a.wapp.ContextMenu.New()
		for _, it := range m.items {
			if it.danger {
				menu.AddSeparator()
			}
			menu.Add(it.label).OnClick(func(ctx *application.Context) {
				a.menuClicked(m.name, it.action, ctx.ContextMenuData())
			})
		}
		a.wapp.ContextMenu.Add(m.name, menu)
	}
}

// menuClicked forwards a menu click to the frontend.
func (a *App) menuClicked(menu, action, data string) {
	a.events().Emit(menuActionEvent, MenuActionDTO{Menu: menu, Action: action, ID: strings.TrimSpace(data)})
}
