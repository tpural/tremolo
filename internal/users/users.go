// Package users keeps tremolo's local accounts, their sessions and API tokens,
// and teams. Passwords are stored as bcrypt hashes and secrets as SHA-256
// hashes, so the state file never holds anything that signs someone in.
package users

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/state"
)

// Role is what a user may do.
type Role string

// Roles, from least to most.
const (
	// Viewer sees everything and changes nothing.
	Viewer Role = "viewer"
	// Editor also triages alerts, silences and sets maintenance.
	Editor Role = "editor"
	// Admin also manages users and teams.
	Admin Role = "admin"
)

// Roles lists the roles from least to most.
var Roles = []Role{Viewer, Editor, Admin}

// Can reports whether r includes at least want.
func (r Role) Can(want Role) bool { return slices.Index(Roles, r) >= slices.Index(Roles, want) }

// User is an account. Its password hash never leaves this package.
type User struct {
	Name        string           `json:"name"`
	DisplayName string           `json:"displayName,omitempty"`
	Role        Role             `json:"role"`
	Contacts    []alerts.Contact `json:"contacts"`
	Created     time.Time        `json:"created"`
}

// Team is a group of users that alerts can go to. Notify, when set, is where
// the team's alerts go (channels or escalations); otherwise each member is
// told through their own contacts.
type Team struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
	Notify  []string `json:"notify,omitempty"`
}

// Token is an API token's description; its secret is shown only once.
type Token struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	User     string    `json:"user"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
	Hash     string    `json:"hash,omitempty"`
}

type account struct {
	User
	PasswordHash string `json:"passwordHash"`
}

type session struct {
	User    string    `json:"user"`
	Expires time.Time `json:"expires"`
}

type data struct {
	Users    map[string]*account `json:"users"`
	Teams    map[string]*Team    `json:"teams"`
	Sessions map[string]session  `json:"sessions"`
	Tokens   []Token             `json:"tokens"`
}

// SessionLength is how long a sign-in lasts.
const SessionLength = 30 * 24 * time.Hour

// TokenPrefix starts every API token, so they're easy to spot.
const TokenPrefix = "tremolo_" //nolint:gosec // a prefix, not a credential

var (
	// ErrExists means the name is taken.
	ErrExists = errors.New("that name is taken")
	// ErrNotFound means no user, team or token has that name.
	ErrNotFound = errors.New("not found")
	// ErrBadLogin means the name or password is wrong.
	ErrBadLogin = errors.New("wrong name or password")
	// ErrLocked means too many wrong passwords; try again in a minute.
	ErrLocked = errors.New("too many attempts; try again in a minute")
	// ErrLastAdmin means the change would leave no admin.
	ErrLastAdmin = errors.New("tremolo needs at least one admin")

	validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
)

// Directory is the accounts and teams.
type Directory struct {
	store *state.Store[data]
	now   func() time.Time
	// dummy is compared against when a user doesn't exist, so a login takes
	// as long either way.
	dummy []byte

	mu       sync.Mutex
	failures map[string][]time.Time
}

// Open loads the directory from db; a nil db keeps it in memory.
func Open(db *state.DB) (*Directory, error) {
	store, err := state.Load[data](db, "users")
	if err != nil {
		return nil, err
	}
	dummy, _ := bcrypt.GenerateFromPassword([]byte(secret(8)), bcrypt.DefaultCost)
	return &Directory{store: store, now: time.Now, dummy: dummy, failures: map[string][]time.Time{}}, nil
}

// Bootstrap creates an admin named "admin" when there are no users yet, with
// password, or a random one it returns.
func (d *Directory) Bootstrap(password string) (string, error) {
	empty := false
	d.store.Read(func(s *data) { empty = len(s.Users) == 0 })
	if !empty {
		return "", nil
	}
	if password != "" {
		return "", d.Create("admin", "", Admin, password)
	}
	pw := secret(12)
	return pw, d.Create("admin", "", Admin, pw)
}

// ResetAdmin is for when every admin is locked out: it makes "admin" an
// admin again with password, or a random one it returns, and ends its sessions.
func (d *Directory) ResetAdmin(password string) (string, error) {
	pw := password
	if pw == "" {
		pw = secret(12)
	}
	if _, ok := d.Get("admin"); !ok {
		if err := d.Create("admin", "", Admin, pw); err != nil {
			return "", err
		}
	} else {
		role := Admin
		if err := d.Update("admin", nil, &role, nil); err != nil {
			return "", err
		}
		if err := d.SetPassword("admin", pw); err != nil {
			return "", err
		}
	}
	if password != "" {
		return "", nil
	}
	return pw, nil
}

// CheckName returns an error unless name is usable for a user or team.
func CheckName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("%q: use 1 to 32 lowercase letters, digits, dots, dashes or underscores", name)
	}
	return nil
}

// CheckPassword returns an error unless pw is long enough.
func CheckPassword(pw string) error {
	if n := len([]rune(pw)); n < 8 || len(pw) > 72 {
		return errors.New("passwords are 8 to 72 characters")
	}
	return nil
}

// Create adds a user.
func (d *Directory) Create(name, displayName string, role Role, password string) error {
	if err := CheckName(name); err != nil {
		return err
	}
	if !slices.Contains(Roles, role) {
		return fmt.Errorf("role %q is not one of %v", role, Roles)
	}
	if err := CheckPassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return d.store.Update(func(s *data) error {
		if _, ok := s.Users[name]; ok {
			return ErrExists
		}
		if _, ok := s.Teams[name]; ok {
			return ErrExists
		}
		if s.Users == nil {
			s.Users = map[string]*account{}
		}
		s.Users[name] = &account{User: User{Name: name, DisplayName: displayName, Role: role, Contacts: []alerts.Contact{}, Created: d.now()}, PasswordHash: string(hash)}
		return nil
	})
}

// Get returns a user.
func (d *Directory) Get(name string) (User, bool) {
	var u User
	var ok bool
	d.store.Read(func(s *data) {
		var a *account
		if a, ok = s.Users[name]; ok {
			u = copyUser(a.User)
		}
	})
	return u, ok
}

// List returns every user, by name.
func (d *Directory) List() []User {
	out := []User{}
	d.store.Read(func(s *data) {
		for _, a := range s.Users {
			out = append(out, copyUser(a.User))
		}
	})
	slices.SortFunc(out, func(a, b User) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func copyUser(u User) User {
	u.Contacts = slices.Clone(u.Contacts)
	if u.Contacts == nil {
		u.Contacts = []alerts.Contact{}
	}
	return u
}

// Update changes a user's display name, role or contacts; nil leaves one as it is.
func (d *Directory) Update(name string, displayName *string, role *Role, contacts []alerts.Contact) error {
	if role != nil && !slices.Contains(Roles, *role) {
		return fmt.Errorf("role %q is not one of %v", *role, Roles)
	}
	for _, c := range contacts {
		if c.Channel == "" || len(c.To) > 200 || c.After.Duration < 0 {
			return errors.New("each contact needs a channel, an address under 200 characters and a delay of 0 or more")
		}
	}
	return d.store.Update(func(s *data) error {
		a, ok := s.Users[name]
		if !ok {
			return ErrNotFound
		}
		if displayName != nil {
			a.DisplayName = strings.TrimSpace(*displayName)
		}
		if role != nil {
			if a.Role == Admin && *role != Admin && admins(s) == 1 {
				return ErrLastAdmin
			}
			a.Role = *role
		}
		if contacts != nil {
			a.Contacts = contacts
		}
		return nil
	})
}

func admins(s *data) int {
	n := 0
	for _, a := range s.Users {
		if a.Role == Admin {
			n++
		}
	}
	return n
}

// SetPassword replaces a user's password and signs them out everywhere.
func (d *Directory) SetPassword(name, password string) error {
	if err := CheckPassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return d.store.Update(func(s *data) error {
		a, ok := s.Users[name]
		if !ok {
			return ErrNotFound
		}
		a.PasswordHash = string(hash)
		for k, se := range s.Sessions {
			if se.User == name {
				delete(s.Sessions, k)
			}
		}
		return nil
	})
}

// ChangePassword replaces a user's password after checking the old one.
func (d *Directory) ChangePassword(name, old, password string) error {
	var hash []byte
	d.store.Read(func(s *data) {
		if a, ok := s.Users[name]; ok {
			hash = []byte(a.PasswordHash)
		}
	})
	if hash == nil || bcrypt.CompareHashAndPassword(hash, []byte(old)) != nil {
		return ErrBadLogin
	}
	return d.SetPassword(name, password)
}

// Delete removes a user, their sessions and tokens, and them from teams.
func (d *Directory) Delete(name string) error {
	return d.store.Update(func(s *data) error {
		a, ok := s.Users[name]
		if !ok {
			return ErrNotFound
		}
		if a.Role == Admin && admins(s) == 1 {
			return ErrLastAdmin
		}
		delete(s.Users, name)
		for k, se := range s.Sessions {
			if se.User == name {
				delete(s.Sessions, k)
			}
		}
		s.Tokens = slices.DeleteFunc(s.Tokens, func(t Token) bool { return t.User == name })
		for _, t := range s.Teams {
			t.Members = slices.DeleteFunc(t.Members, func(m string) bool { return m == name })
		}
		return nil
	})
}

// Login checks a password and starts a session, returning its secret. After
// five wrong passwords in a minute, a name is locked for a minute.
func (d *Directory) Login(name, password string) (string, User, error) {
	now := d.now()
	d.mu.Lock()
	recent := slices.DeleteFunc(d.failures[name], func(t time.Time) bool { return now.Sub(t) > time.Minute })
	d.failures[name] = recent
	locked := len(recent) >= 5
	d.mu.Unlock()
	if locked {
		return "", User{}, ErrLocked
	}
	var hash []byte
	var u User
	d.store.Read(func(s *data) {
		if a, ok := s.Users[name]; ok {
			hash, u = []byte(a.PasswordHash), copyUser(a.User)
		}
	})
	if hash == nil {
		_ = bcrypt.CompareHashAndPassword(d.dummy, []byte(password))
		d.fail(name, now)
		return "", User{}, ErrBadLogin
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		d.fail(name, now)
		return "", User{}, ErrBadLogin
	}
	d.mu.Lock()
	delete(d.failures, name)
	d.mu.Unlock()
	sec := secret(32)
	err := d.store.Update(func(s *data) error {
		if s.Sessions == nil {
			s.Sessions = map[string]session{}
		}
		for k, se := range s.Sessions {
			if now.After(se.Expires) {
				delete(s.Sessions, k)
			}
		}
		s.Sessions[hash256(sec)] = session{User: name, Expires: now.Add(SessionLength)}
		return nil
	})
	return sec, u, err
}

func (d *Directory) fail(name string, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.failures) > 10000 {
		clear(d.failures)
	}
	d.failures[name] = append(d.failures[name], now)
}

// Logout ends a session.
func (d *Directory) Logout(sessionSecret string) {
	_ = d.store.Update(func(s *data) error {
		delete(s.Sessions, hash256(sessionSecret))
		return nil
	})
}

// Session returns the user a session secret belongs to.
func (d *Directory) Session(sessionSecret string) (User, bool) {
	now := d.now()
	var u User
	ok := false
	d.store.Read(func(s *data) {
		se, found := s.Sessions[hash256(sessionSecret)]
		if !found || now.After(se.Expires) {
			return
		}
		if a, found := s.Users[se.User]; found {
			u, ok = copyUser(a.User), true
		}
	})
	return u, ok
}

// CreateToken makes an API token for a user and returns its secret.
func (d *Directory) CreateToken(user, name string) (string, Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return "", Token{}, errors.New("a token needs a name of up to 100 characters")
	}
	sec := TokenPrefix + secret(24)
	t := Token{ID: secret(6), Name: name, User: user, Created: d.now(), Hash: hash256(sec)}
	err := d.store.Update(func(s *data) error {
		if _, ok := s.Users[user]; !ok {
			return ErrNotFound
		}
		s.Tokens = append(s.Tokens, t)
		return nil
	})
	t.Hash = ""
	return sec, t, err
}

// Tokens lists a user's API tokens.
func (d *Directory) Tokens(user string) []Token {
	out := []Token{}
	d.store.Read(func(s *data) {
		for _, t := range s.Tokens {
			if t.User == user {
				t.Hash = ""
				out = append(out, t)
			}
		}
	})
	return out
}

// DeleteToken removes one of a user's API tokens.
func (d *Directory) DeleteToken(user, id string) error {
	return d.store.Update(func(s *data) error {
		n := len(s.Tokens)
		s.Tokens = slices.DeleteFunc(s.Tokens, func(t Token) bool { return t.User == user && t.ID == id })
		if len(s.Tokens) == n {
			return ErrNotFound
		}
		return nil
	})
}

// TokenUser returns the user an API token belongs to, noting when it was used.
func (d *Directory) TokenUser(sec string) (User, bool) {
	h := hash256(sec)
	var u User
	ok, stale := false, false
	now := d.now()
	d.store.Read(func(s *data) {
		for _, t := range s.Tokens {
			if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(h)) == 1 {
				if a, found := s.Users[t.User]; found {
					u, ok = copyUser(a.User), true
					stale = now.Sub(t.LastUsed) > time.Hour
				}
				return
			}
		}
	})
	if stale {
		// Note use at most hourly, so scripts don't rewrite the file on every call.
		_ = d.store.Update(func(s *data) error {
			for i := range s.Tokens {
				if s.Tokens[i].Hash == h {
					s.Tokens[i].LastUsed = now
				}
			}
			return nil
		})
	}
	return u, ok
}

// Teams returns every team, by name.
func (d *Directory) Teams() []Team {
	out := []Team{}
	d.store.Read(func(s *data) {
		for _, t := range s.Teams {
			c := *t
			c.Members, c.Notify = slices.Clone(t.Members), slices.Clone(t.Notify)
			if c.Members == nil {
				c.Members = []string{}
			}
			out = append(out, c)
		}
	})
	slices.SortFunc(out, func(a, b Team) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// SaveTeam creates or replaces a team.
func (d *Directory) SaveTeam(t Team) error {
	if err := CheckName(t.Name); err != nil {
		return err
	}
	slices.Sort(t.Members)
	t.Members = slices.Compact(t.Members)
	return d.store.Update(func(s *data) error {
		if _, ok := s.Users[t.Name]; ok {
			return ErrExists
		}
		for _, m := range t.Members {
			if _, ok := s.Users[m]; !ok {
				return fmt.Errorf("no user %q", m)
			}
		}
		if s.Teams == nil {
			s.Teams = map[string]*Team{}
		}
		s.Teams[t.Name] = &t
		return nil
	})
}

// DeleteTeam removes a team.
func (d *Directory) DeleteTeam(name string) error {
	return d.store.Update(func(s *data) error {
		if _, ok := s.Teams[name]; !ok {
			return ErrNotFound
		}
		delete(s.Teams, name)
		return nil
	})
}

// Person implements alerts.Directory.
func (d *Directory) Person(name string) ([]alerts.Contact, bool) {
	u, ok := d.Get(name)
	return u.Contacts, ok
}

// Team implements alerts.Directory.
func (d *Directory) Team(name string) (members, notify []string, ok bool) {
	d.store.Read(func(s *data) {
		var t *Team
		if t, ok = s.Teams[name]; ok {
			members, notify = slices.Clone(t.Members), slices.Clone(t.Notify)
		}
	})
	return members, notify, ok
}

// Names implements alerts.Directory.
func (d *Directory) Names() (people, teams []string) {
	d.store.Read(func(s *data) {
		for n := range s.Users {
			people = append(people, n)
		}
		for n := range s.Teams {
			teams = append(teams, n)
		}
	})
	slices.Sort(people)
	slices.Sort(teams)
	return people, teams
}

// TeamsOf lists the teams a user is in.
func (d *Directory) TeamsOf(name string) []string {
	out := []string{}
	d.store.Read(func(s *data) {
		for _, t := range s.Teams {
			if slices.Contains(t.Members, name) {
				out = append(out, t.Name)
			}
		}
	})
	slices.Sort(out)
	return out
}

func secret(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hash256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
