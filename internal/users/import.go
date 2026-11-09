package users

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/state"
)

// old is how tremolo 0.7 kept users: one JSON document in the state table
// (or users.json before that).
type old struct {
	Users map[string]struct {
		User
		PasswordHash string `json:"passwordHash"`
	} `json:"users"`
	Teams    map[string]Team `json:"teams"`
	Sessions map[string]struct {
		User    string    `json:"user"`
		Expires time.Time `json:"expires"`
	} `json:"sessions"`
	Tokens []struct {
		Token
		Hash string `json:"hash"`
	} `json:"tokens"`
}

// importOld moves users kept the old way into their tables, once. Signed-in
// sessions and API tokens keep working.
func (d *Directory) importOld(db *state.DB) error {
	o, err := state.Load[old](db, "users")
	if err != nil {
		return err
	}
	var v old
	o.Read(func(x *old) { v = *x })
	if len(v.Users) == 0 {
		return nil
	}
	return d.tx(func(tx *sql.Tx) error {
		for name, u := range v.Users {
			if _, err := tx.Exec(`INSERT INTO users (name, display_name, role, password_hash, created) VALUES (?, ?, ?, ?, ?)`,
				name, u.DisplayName, u.Role, u.PasswordHash, ms(u.Created)); err != nil {
				return err
			}
			if err := setContacts(tx, name, nonNilContacts(u.Contacts)); err != nil {
				return err
			}
		}
		for name, t := range v.Teams {
			notify, _ := json.Marshal(nonNil(t.Notify))
			if _, err := tx.Exec(`INSERT INTO teams (name, notify, created) VALUES (?, ?, ?)`, name, string(notify), ms(d.now())); err != nil {
				return err
			}
			for _, m := range t.Members {
				if _, ok := v.Users[m]; !ok {
					continue
				}
				if _, err := tx.Exec(`INSERT OR IGNORE INTO team_members (team, user) VALUES (?, ?)`, name, m); err != nil {
					return err
				}
			}
		}
		now := d.now()
		for hash, s := range v.Sessions {
			if _, ok := v.Users[s.User]; !ok || s.Expires.Before(now) {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO sessions (id, hash, user, created, expires, last_seen) VALUES (?, ?, ?, ?, ?, ?)`,
				secret(9), hash, s.User, ms(now), ms(s.Expires), ms(now)); err != nil {
				return err
			}
		}
		for _, t := range v.Tokens {
			if _, ok := v.Users[t.User]; !ok {
				continue
			}
			var used any
			if !t.LastUsed.IsZero() {
				used = ms(t.LastUsed)
			}
			if _, err := tx.Exec(`INSERT INTO tokens (id, hash, user, name, created, last_used) VALUES (?, ?, ?, ?, ?, ?)`,
				t.ID, t.Hash, t.User, t.Name, ms(t.Created), used); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`DELETE FROM state WHERE name = 'users'`)
		return err
	})
}

func nonNilContacts(c []alerts.Contact) []alerts.Contact {
	if c == nil {
		return []alerts.Contact{}
	}
	return c
}
