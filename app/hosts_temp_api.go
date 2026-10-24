package app

// TEMP(Task 11): a minimal host listing for the dev "connect" button; the hosts API replaces it.

// HostDTO is a saved host as the temporary dev button needs it.
type HostDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	KeyName string `json:"key_name"`
}

// HostsListTemp lists saved hosts. TEMP(Task 11).
func (a *App) HostsListTemp() []HostDTO {
	out := []HostDTO{}
	if !a.storeUnlocked() {
		return out
	}
	names := map[string]string{}
	for _, k := range a.store.Keys() {
		names[k.ID] = k.Name
	}
	for _, h := range a.store.Hosts() {
		out = append(out, HostDTO{ID: h.ID, Name: h.Name, Address: h.Address, Port: h.Port, User: h.User, KeyName: names[h.KeyID]})
	}
	return out
}
