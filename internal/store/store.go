package store

import (
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"sshclient/internal/model"
)

// ErrKeyInUse is returned when deleting a key that hosts still reference.
var ErrKeyInUse = errors.New("key is used by hosts")

var errStoreLocked = errors.New("vault is locked")

// Store gives typed access to the vault contents; every mutation is saved.
type Store struct {
	mu sync.Mutex
	v  *Vault
}

// NewStore wraps an open vault.
func NewStore(v *Vault) *Store { return &Store{v: v} }

// read runs f on the data under the lock; zero result when locked.
func read[T any](s *Store, f func(d *model.VaultData) T) T {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.v.Data()
	if d == nil {
		var zero T
		return zero
	}
	return f(d)
}

// mutate runs f on the data and saves; a failure rolls back in memory.
func (s *Store) mutate(f func(d *model.VaultData) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.v.Data()
	if d == nil {
		return errStoreLocked
	}
	snapshot := *d
	snapshot.Hosts = slices.Clone(d.Hosts)
	snapshot.Groups = slices.Clone(d.Groups)
	snapshot.Keys = slices.Clone(d.Keys)
	snapshot.Forwards = slices.Clone(d.Forwards)
	if err := f(d); err != nil {
		*d = snapshot
		return err
	}
	if err := s.v.Save(); err != nil {
		*d = snapshot
		return err
	}
	return nil
}

// Hosts returns a copy of all hosts.
func (s *Store) Hosts() []model.Host {
	return read(s, func(d *model.VaultData) []model.Host { return slices.Clone(d.Hosts) })
}

// SaveHost inserts or updates a host and returns the stored value.
func (s *Store) SaveHost(h model.Host) (model.Host, error) {
	err := s.mutate(func(d *model.VaultData) error {
		if h.Name == "" {
			h.Name = h.Address
		}
		if i := slices.IndexFunc(d.Hosts, func(x model.Host) bool { return h.ID != "" && x.ID == h.ID }); i >= 0 {
			h.CreatedAt = d.Hosts[i].CreatedAt
			d.Hosts[i] = h
			return nil
		}
		if h.ID == "" {
			h.ID = uuid.NewString()
		}
		h.CreatedAt = time.Now().UTC()
		d.Hosts = append(d.Hosts, h)
		return nil
	})
	return h, err
}

// DeleteHost removes a host; unknown ids are ignored.
func (s *Store) DeleteHost(id string) error {
	return s.mutate(func(d *model.VaultData) error {
		d.Hosts = slices.DeleteFunc(d.Hosts, func(h model.Host) bool { return h.ID == id })
		return nil
	})
}

// HostsUsingKey returns the hosts that reference keyID.
func (s *Store) HostsUsingKey(keyID string) []model.Host {
	return read(s, func(d *model.VaultData) []model.Host {
		var out []model.Host
		for _, h := range d.Hosts {
			if h.KeyID == keyID {
				out = append(out, h)
			}
		}
		return out
	})
}

// Groups returns a copy of all groups.
func (s *Store) Groups() []model.Group {
	return read(s, func(d *model.VaultData) []model.Group { return slices.Clone(d.Groups) })
}

// SaveGroup inserts or updates a group.
func (s *Store) SaveGroup(g model.Group) (model.Group, error) {
	err := s.mutate(func(d *model.VaultData) error {
		if i := slices.IndexFunc(d.Groups, func(x model.Group) bool { return g.ID != "" && x.ID == g.ID }); i >= 0 {
			d.Groups[i] = g
			return nil
		}
		if g.ID == "" {
			g.ID = uuid.NewString()
		}
		d.Groups = append(d.Groups, g)
		return nil
	})
	return g, err
}

// DeleteGroup removes a group and ungroups its hosts.
func (s *Store) DeleteGroup(id string) error {
	return s.mutate(func(d *model.VaultData) error {
		d.Groups = slices.DeleteFunc(d.Groups, func(g model.Group) bool { return g.ID == id })
		for i := range d.Hosts {
			if d.Hosts[i].GroupID == id {
				d.Hosts[i].GroupID = ""
			}
		}
		return nil
	})
}

// Keys returns a copy of all keys.
func (s *Store) Keys() []model.Key {
	return read(s, func(d *model.VaultData) []model.Key { return slices.Clone(d.Keys) })
}

// SaveKey inserts or updates a key.
func (s *Store) SaveKey(k model.Key) (model.Key, error) {
	err := s.mutate(func(d *model.VaultData) error {
		if i := slices.IndexFunc(d.Keys, func(x model.Key) bool { return k.ID != "" && x.ID == k.ID }); i >= 0 {
			k.CreatedAt = d.Keys[i].CreatedAt
			d.Keys[i] = k
			return nil
		}
		if k.ID == "" {
			k.ID = uuid.NewString()
		}
		if k.CreatedAt.IsZero() {
			k.CreatedAt = time.Now().UTC()
		}
		d.Keys = append(d.Keys, k)
		return nil
	})
	return k, err
}

// DeleteKey removes a key, or returns ErrKeyInUse if hosts reference it.
func (s *Store) DeleteKey(id string) error {
	return s.mutate(func(d *model.VaultData) error {
		if slices.ContainsFunc(d.Hosts, func(h model.Host) bool { return h.KeyID == id }) {
			return ErrKeyInUse
		}
		d.Keys = slices.DeleteFunc(d.Keys, func(k model.Key) bool { return k.ID == id })
		return nil
	})
}

// Forwards returns a copy of all forwarding rules.
func (s *Store) Forwards() []model.Forward {
	return read(s, func(d *model.VaultData) []model.Forward { return slices.Clone(d.Forwards) })
}

// SaveForward inserts or updates a forwarding rule.
func (s *Store) SaveForward(f model.Forward) (model.Forward, error) {
	err := s.mutate(func(d *model.VaultData) error {
		if i := slices.IndexFunc(d.Forwards, func(x model.Forward) bool { return f.ID != "" && x.ID == f.ID }); i >= 0 {
			d.Forwards[i] = f
			return nil
		}
		if f.ID == "" {
			f.ID = uuid.NewString()
		}
		d.Forwards = append(d.Forwards, f)
		return nil
	})
	return f, err
}

// DeleteForward removes a forwarding rule.
func (s *Store) DeleteForward(id string) error {
	return s.mutate(func(d *model.VaultData) error {
		d.Forwards = slices.DeleteFunc(d.Forwards, func(f model.Forward) bool { return f.ID == id })
		return nil
	})
}

// Settings returns the user settings.
func (s *Store) Settings() model.Settings {
	return read(s, func(d *model.VaultData) model.Settings { return d.Settings })
}

// SaveSettings replaces the user settings.
func (s *Store) SaveSettings(set model.Settings) error {
	return s.mutate(func(d *model.VaultData) error {
		d.Settings = set
		return nil
	})
}
