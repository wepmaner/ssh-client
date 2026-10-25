package app

import (
	"cmp"
	"errors"
	"slices"
	"strings"

	"sshclient/internal/model"
)

// Validation errors name the form field as field:<name>: so the dialog can show
// the text under it; the rest of the message is shown verbatim.
const fieldErrorPrefix = "field:"

func fieldError(field, text string) error {
	return errors.New(fieldErrorPrefix + field + ": " + text)
}

// User-facing messages of the hosts API.
//
//nolint:staticcheck // ST1005: Russian UI text starts with a capital letter
var (
	errGroupNotFound = errors.New("Группа не найдена")
)

const defaultSSHPort = 22

// HostDTO is a saved host as shown in the UI. OS is ubuntu|debian|other or
// empty and only picks the monogram colour. Connected means a session to the
// host is in the connected state.
type HostDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	GroupID   string `json:"group_id"`
	GroupName string `json:"group_name"`
	KeyID     string `json:"key_id"`
	KeyName   string `json:"key_name"`
	OS        string `json:"os"`
	Connected bool   `json:"connected"`
}

// GroupDTO is a host group with its member counts.
type GroupDTO struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	HostCount      int    `json:"host_count"`
	ConnectedCount int    `json:"connected_count"`
}

// connectedHosts returns the IDs of hosts with a session in the connected state.
func (a *App) connectedHosts() map[string]bool {
	a.sessMu.Lock()
	all := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		all = append(all, s)
	}
	a.sessMu.Unlock()
	out := map[string]bool{}
	for _, s := range all {
		if d := s.dto(); d.State == stateConnected {
			out[d.HostID] = true
		}
	}
	return out
}

func byName[T any](name func(T) string) func(x, y T) int {
	return func(x, y T) int { return cmp.Compare(strings.ToLower(name(x)), strings.ToLower(name(y))) }
}

// HostsList returns all saved hosts sorted by name.
func (a *App) HostsList() []HostDTO {
	out := []HostDTO{}
	if !a.storeUnlocked() {
		return out
	}
	keyNames := map[string]string{}
	for _, k := range a.storeRef().KeyMetas() {
		keyNames[k.ID] = k.Name
	}
	groupNames := map[string]string{}
	for _, g := range a.storeRef().Groups() {
		groupNames[g.ID] = g.Name
	}
	connected := a.connectedHosts()
	for _, h := range a.storeRef().Hosts() {
		out = append(out, HostDTO{
			ID: h.ID, Name: h.Name, Address: h.Address, Port: h.Port, User: h.User,
			GroupID: h.GroupID, GroupName: groupNames[h.GroupID], KeyID: h.KeyID, KeyName: keyNames[h.KeyID],
			OS: h.OS, Connected: connected[h.ID],
		})
	}
	slices.SortStableFunc(out, byName(func(h HostDTO) string { return h.Name }))
	return out
}

func (a *App) hostDTO(id string) (HostDTO, error) {
	for _, h := range a.HostsList() {
		if h.ID == id {
			return h, nil
		}
	}
	return HostDTO{}, errHostNotFound
}

// normalizeHost trims the connection fields and checks them: port 0 means 22, an empty name
// means the address. The first problem is returned as a field error. Imports use it without
// the store checks of validateHost.
func normalizeHost(in HostDTO) (model.Host, error) {
	h := model.Host{
		ID: in.ID, Name: strings.TrimSpace(in.Name), Address: strings.TrimSpace(in.Address), Port: in.Port,
		User: strings.TrimSpace(in.User), GroupID: in.GroupID, KeyID: in.KeyID, OS: in.OS,
	}
	if h.Port == 0 {
		h.Port = defaultSSHPort
	}
	switch {
	case h.Address == "":
		return h, fieldError("address", "Укажите адрес сервера")
	case strings.ContainsAny(h.Address, " \t"):
		return h, fieldError("address", "Адрес не должен содержать пробелов")
	case h.Port < 1 || h.Port > 65535:
		return h, fieldError("port", "Порт от 1 до 65535")
	case h.User == "":
		return h, fieldError("user", "Укажите пользователя")
	}
	if h.Name == "" {
		h.Name = h.Address
	}
	return h, nil
}

// validateHost checks the form values of the host dialog; the first problem is returned as a
// field error.
func (a *App) validateHost(in HostDTO) (model.Host, error) {
	h, err := normalizeHost(in)
	if err != nil {
		return h, err
	}
	// The key is optional: without one the server asks for the password in the console.
	if _, err := a.keyMeta(h.KeyID); h.KeyID != "" && err != nil {
		return h, fieldError("key", errKeyNotFound.Error())
	}
	if h.GroupID != "" && !slices.ContainsFunc(a.storeRef().Groups(), func(g model.Group) bool { return g.ID == h.GroupID }) {
		return h, errGroupNotFound
	}
	if h.ID != "" && !slices.ContainsFunc(a.storeRef().Hosts(), func(x model.Host) bool { return x.ID == h.ID }) {
		return h, errHostNotFound
	}
	return h, nil
}

// HostSave creates a host (empty ID) or updates an existing one. Port 0 means
// 22, an empty name means the address.
func (a *App) HostSave(in HostDTO) (HostDTO, error) {
	if !a.storeUnlocked() {
		return HostDTO{}, errVaultLocked
	}
	h, err := a.validateHost(in)
	if err != nil {
		return HostDTO{}, err
	}
	saved, err := a.storeRef().SaveHost(h)
	if err != nil {
		return HostDTO{}, err
	}
	return a.hostDTO(saved.ID)
}

// HostDelete removes the host and then closes its sessions and stops and removes its
// forwards; if the store fails, the sessions and forwards stay as they were.
func (a *App) HostDelete(id string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if err := a.storeRef().DeleteHost(id); err != nil {
		return err
	}
	a.closeSessionsOfHost(id)
	return a.deleteForwardsOfHost(id)
}

// GroupsList returns all groups sorted by name with their host counts.
func (a *App) GroupsList() []GroupDTO {
	out := []GroupDTO{}
	if !a.storeUnlocked() {
		return out
	}
	hosts := a.HostsList()
	for _, g := range a.storeRef().Groups() {
		d := GroupDTO{ID: g.ID, Name: g.Name}
		for _, h := range hosts {
			if h.GroupID == g.ID {
				d.HostCount++
				if h.Connected {
					d.ConnectedCount++
				}
			}
		}
		out = append(out, d)
	}
	slices.SortStableFunc(out, byName(func(g GroupDTO) string { return g.Name }))
	return out
}

// checkGroupName validates a group name; exceptID is the group being renamed.
func (a *App) checkGroupName(name, exceptID string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fieldError("name", "Укажите название группы")
	}
	for _, g := range a.storeRef().Groups() {
		if g.ID != exceptID && strings.EqualFold(g.Name, name) {
			return "", fieldError("name", "Группа с таким названием уже есть")
		}
	}
	return name, nil
}

// GroupSave creates a group.
func (a *App) GroupSave(name string) (GroupDTO, error) {
	if !a.storeUnlocked() {
		return GroupDTO{}, errVaultLocked
	}
	name, err := a.checkGroupName(name, "")
	if err != nil {
		return GroupDTO{}, err
	}
	g, err := a.storeRef().SaveGroup(model.Group{Name: name})
	if err != nil {
		return GroupDTO{}, err
	}
	return GroupDTO{ID: g.ID, Name: g.Name}, nil
}

// GroupRename changes the name of a group.
func (a *App) GroupRename(id, name string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	if !slices.ContainsFunc(a.storeRef().Groups(), func(g model.Group) bool { return g.ID == id }) {
		return errGroupNotFound
	}
	name, err := a.checkGroupName(name, id)
	if err != nil {
		return err
	}
	_, err = a.storeRef().SaveGroup(model.Group{ID: id, Name: name})
	return err
}

// GroupDelete removes a group; its hosts stay, without a group.
func (a *App) GroupDelete(id string) error {
	if !a.storeUnlocked() {
		return errVaultLocked
	}
	return a.storeRef().DeleteGroup(id)
}
