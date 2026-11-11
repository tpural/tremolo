// Package users keeps tremolo's accounts, their sessions and API tokens, and
// teams, in tremolo's database. Passwords are stored as bcrypt hashes and
// secrets as SHA-256 hashes, so the database never holds anything that signs
// someone in.
package users

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

// Token is an API token's description; its secret is shown only once. A
// token with a Scope is an assistant's: it may do only what the scope allows,
// and never more than its user's role. Without one it acts as its user.
type Token struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	User     string    `json:"user"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
	Expires  time.Time `json:"expires,omitzero"`
	Scope    *Scope    `json:"scope,omitempty"`
}

// Expired reports whether t has expired by now.
func (t Token) Expired(now time.Time) bool { return !t.Expires.IsZero() && !now.Before(t.Expires) }

// Session is where someone is signed in.
type Session struct {
	ID       string    `json:"id"`
	Provider string    `json:"provider"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
	Expires  time.Time `json:"expires"`
	Address  string    `json:"address,omitempty"`
	Agent    string    `json:"agent,omitempty"`
	// Current is the session asking.
	Current bool `json:"current,omitempty"`
}

// Client is who is signing in: their address and browser, shown in their
// list of sessions.
type Client struct {
	Address string
	Agent   string
}

// SessionLength is how long a sign-in lasts.
const SessionLength = 30 * 24 * time.Hour

// TokenPrefix starts every API token, so they're easy to spot.
const TokenPrefix = "tremolo_" //nolint:gosec // a prefix, not a credential

// seenEvery is how often a session's or token's last use is written, so
// every request doesn't write to the database.
const seenEvery = 5 * time.Minute

var (
	// ErrExists means the name is taken.
	ErrExists = errors.New("that name is taken")
	// ErrSetUp means tremolo already has users, so setup is closed.
	ErrSetUp = errors.New("tremolo is already set up; sign in instead")
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
	db  *sql.DB
	now func() time.Time
	// dummy is compared against when a user doesn't exist, so a login takes
	// as long either way.
	dummy []byte

	mu       sync.Mutex
	failures map[string][]time.Time
}

// Open returns the directory kept in db; a nil db keeps it in memory.
func Open(db *state.DB) (*Directory, error) {
	if db == nil {
		var err error
		if db, err = state.Open(""); err != nil {
			return nil, err
		}
	}
	dummy, _ := bcrypt.GenerateFromPassword([]byte(secret(8)), bcrypt.DefaultCost)
	d := &Directory{db: db.SQL(), now: time.Now, dummy: dummy, failures: map[string][]time.Time{}}
	if err := d.importOld(db); err != nil {
		return nil, fmt.Errorf("importing users: %w", err)
	}
	return d, nil
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func at(v int64) time.Time { return time.UnixMilli(v) }

func atNull(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return at(v.Int64)
}

// tx runs fn in a transaction, committing when it returns nil.
func (d *Directory) tx(fn func(*sql.Tx) error) error {
	tx, err := d.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

type querier interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

func exists(q querier, query string, args ...any) (bool, error) {
	var one int
	err := q.QueryRow(query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// taken reports whether a user or team already has name; they share names,
// since alerts address both the same way.
func taken(q querier, name string) (bool, error) {
	return exists(q, `SELECT 1 FROM users WHERE name = ? UNION SELECT 1 FROM teams WHERE name = ?`, name, name)
}

func countAdmins(q querier) (int, error) {
	var n int
	err := q.QueryRow(`SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&n)
	return n, err
}

// Bootstrap creates an admin named "admin" with password when there are no
// users yet. Without a password, the first person to open tremolo sets one up.
func (d *Directory) Bootstrap(password string) error {
	if password == "" || !d.Empty() {
		return nil
	}
	return d.Create("admin", "", Admin, password)
}

// Empty reports whether there are no users yet, so tremolo needs setting up.
func (d *Directory) Empty() bool {
	found, err := exists(d.db, `SELECT 1 FROM users LIMIT 1`)
	return err == nil && !found
}

// Setup creates the first user, an admin. It fails once anyone exists, so
// only the first person to open tremolo can do it.
func (d *Directory) Setup(name, password string) error {
	return d.create(name, "", Admin, password, true)
}

// ResetAdmin is for when every admin is locked out: it makes "admin" an
// admin again with password, and ends its sessions.
func (d *Directory) ResetAdmin(password string) error {
	if password == "" {
		return errors.New("set the new admin password in $TREMOLO_ADMIN_PASSWORD")
	}
	if _, ok := d.Get("admin"); !ok {
		return d.Create("admin", "", Admin, password)
	}
	role := Admin
	if err := d.Update("admin", nil, &role, nil); err != nil {
		return err
	}
	return d.SetPassword("admin", password)
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
	return d.create(name, displayName, role, password, false)
}

func (d *Directory) create(name, displayName string, role Role, password string, first bool) error {
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
	return d.tx(func(tx *sql.Tx) error {
		if first {
			if found, err := exists(tx, `SELECT 1 FROM users LIMIT 1`); err != nil || found {
				return cmp(err, ErrSetUp)
			}
		}
		if t, err := taken(tx, name); err != nil || t {
			return cmp(err, ErrExists)
		}
		_, err := tx.Exec(`INSERT INTO users (name, display_name, role, password_hash, created) VALUES (?, ?, ?, ?, ?)`,
			name, strings.TrimSpace(displayName), role, string(hash), ms(d.now()))
		return err
	})
}

// cmp returns err when there is one, else otherwise.
func cmp(err, otherwise error) error {
	if err != nil {
		return err
	}
	return otherwise
}

// Get returns a user.
func (d *Directory) Get(name string) (User, bool) {
	list, err := d.users(name)
	if err != nil || len(list) == 0 {
		return User{}, false
	}
	return list[0], true
}

// List returns every user, by name.
func (d *Directory) List() []User {
	list, _ := d.users("")
	if list == nil {
		list = []User{}
	}
	return list
}

// users returns the user called name, or everyone when name is empty.
func (d *Directory) users(name string) ([]User, error) {
	rows, err := d.db.Query(`SELECT name, display_name, role, created FROM users WHERE ? = '' OR name = ? ORDER BY name`, name, name)
	if err != nil {
		return nil, err
	}
	var out []User
	for rows.Next() {
		var u User
		var created int64
		if err := rows.Scan(&u.Name, &u.DisplayName, &u.Role, &created); err != nil {
			_ = rows.Close()
			return nil, err
		}
		u.Created, u.Contacts = at(created), []alerts.Contact{}
		out = append(out, u)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Contacts, err = d.contacts(out[i].Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (d *Directory) contacts(user string) ([]alerts.Contact, error) {
	rows, err := d.db.Query(`SELECT channel, address, after_ms FROM contacts WHERE user = ? ORDER BY position`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []alerts.Contact{}
	for rows.Next() {
		var c alerts.Contact
		var after int64
		if err := rows.Scan(&c.Channel, &c.To, &after); err != nil {
			return nil, err
		}
		c.After.Duration = time.Duration(after) * time.Millisecond
		out = append(out, c)
	}
	return out, rows.Err()
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
	return d.tx(func(tx *sql.Tx) error {
		var current Role
		if err := tx.QueryRow(`SELECT role FROM users WHERE name = ?`, name).Scan(&current); err != nil {
			return notFound(err)
		}
		if displayName != nil {
			if _, err := tx.Exec(`UPDATE users SET display_name = ? WHERE name = ?`, strings.TrimSpace(*displayName), name); err != nil {
				return err
			}
		}
		if role != nil {
			if current == Admin && *role != Admin {
				if n, err := countAdmins(tx); err != nil || n == 1 {
					return cmp(err, ErrLastAdmin)
				}
			}
			if _, err := tx.Exec(`UPDATE users SET role = ? WHERE name = ?`, *role, name); err != nil {
				return err
			}
		}
		if contacts != nil {
			return setContacts(tx, name, contacts)
		}
		return nil
	})
}

func setContacts(tx *sql.Tx, user string, contacts []alerts.Contact) error {
	if _, err := tx.Exec(`DELETE FROM contacts WHERE user = ?`, user); err != nil {
		return err
	}
	for i, c := range contacts {
		if _, err := tx.Exec(`INSERT INTO contacts (user, position, channel, address, after_ms) VALUES (?, ?, ?, ?, ?)`,
			user, i, c.Channel, c.To, c.After.Milliseconds()); err != nil {
			return err
		}
	}
	return nil
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
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
	return d.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE users SET password_hash = ? WHERE name = ?`, string(hash), name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(`DELETE FROM sessions WHERE user = ?`, name)
		return err
	})
}

// ChangePassword replaces a user's password after checking the old one.
func (d *Directory) ChangePassword(name, old, password string) error {
	hash := d.passwordHash(name)
	if hash == nil || bcrypt.CompareHashAndPassword(hash, []byte(old)) != nil {
		return ErrBadLogin
	}
	return d.SetPassword(name, password)
}

func (d *Directory) passwordHash(name string) []byte {
	var h sql.NullString
	if err := d.db.QueryRow(`SELECT password_hash FROM users WHERE name = ?`, name).Scan(&h); err != nil || !h.Valid {
		return nil
	}
	return []byte(h.String)
}

// Delete removes a user; their contacts, sessions, tokens, identities and
// team memberships go with them.
func (d *Directory) Delete(name string) error {
	return d.tx(func(tx *sql.Tx) error {
		var role Role
		if err := tx.QueryRow(`SELECT role FROM users WHERE name = ?`, name).Scan(&role); err != nil {
			return notFound(err)
		}
		if role == Admin {
			if n, err := countAdmins(tx); err != nil || n == 1 {
				return cmp(err, ErrLastAdmin)
			}
		}
		_, err := tx.Exec(`DELETE FROM users WHERE name = ?`, name)
		return err
	})
}

// Login checks a password and starts a session, returning its secret. After
// five wrong passwords in a minute, a name is locked for a minute.
func (d *Directory) Login(name, password string, c Client) (string, User, error) {
	now := d.now()
	d.mu.Lock()
	recent := slices.DeleteFunc(d.failures[name], func(t time.Time) bool { return now.Sub(t) > time.Minute })
	d.failures[name] = recent
	locked := len(recent) >= 5
	d.mu.Unlock()
	if locked {
		return "", User{}, ErrLocked
	}
	hash := d.passwordHash(name)
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
	u, _ := d.Get(name)
	sec, err := d.StartSession(name, "local", c)
	return sec, u, err
}

// StartSession signs a user in, returning the session's secret. provider is
// how they proved who they are: "local" for a password.
func (d *Directory) StartSession(user, provider string, c Client) (string, error) {
	now := d.now()
	sec := secret(32)
	err := d.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM sessions WHERE expires < ?`, ms(now)); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO sessions (id, hash, user, provider, created, expires, last_seen, address, agent)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			secret(9), hash256(sec), user, provider, ms(now), ms(now.Add(SessionLength)), ms(now), clip(c.Address, 100), clip(c.Agent, 300))
		return err
	})
	return sec, err
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
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
	_, _ = d.db.Exec(`DELETE FROM sessions WHERE hash = ?`, hash256(sessionSecret))
}

// Session returns the user a session secret belongs to.
func (d *Directory) Session(sessionSecret string) (User, bool) {
	now := d.now()
	var name string
	var seen int64
	err := d.db.QueryRow(`SELECT user, last_seen FROM sessions WHERE hash = ? AND expires >= ?`, hash256(sessionSecret), ms(now)).Scan(&name, &seen)
	if err != nil {
		return User{}, false
	}
	if now.Sub(at(seen)) > seenEvery {
		_, _ = d.db.Exec(`UPDATE sessions SET last_seen = ? WHERE hash = ?`, ms(now), hash256(sessionSecret))
	}
	return d.Get(name)
}

// Sessions lists where a user is signed in, newest first; current is the
// asking session's secret, which is marked.
func (d *Directory) Sessions(user, current string) []Session {
	out := []Session{}
	rows, err := d.db.Query(`SELECT id, hash, provider, created, last_seen, expires, address, agent FROM sessions
		WHERE user = ? AND expires >= ? ORDER BY last_seen DESC`, user, ms(d.now()))
	if err != nil {
		return out
	}
	defer rows.Close()
	mine := hash256(current)
	for rows.Next() {
		var s Session
		var hash string
		var created, seen, expires int64
		if rows.Scan(&s.ID, &hash, &s.Provider, &created, &seen, &expires, &s.Address, &s.Agent) != nil {
			return out
		}
		s.Created, s.LastSeen, s.Expires, s.Current = at(created), at(seen), at(expires), hash == mine
		out = append(out, s)
	}
	return out
}

// EndSession signs one of a user's sessions out.
func (d *Directory) EndSession(user, id string) error {
	res, err := d.db.Exec(`DELETE FROM sessions WHERE user = ? AND id = ?`, user, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// EndSessions signs a user out everywhere, returning how many sessions ended.
func (d *Directory) EndSessions(user string) (int64, error) {
	if _, ok := d.Get(user); !ok {
		return 0, ErrNotFound
	}
	res, err := d.db.Exec(`DELETE FROM sessions WHERE user = ?`, user)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CreateToken makes an API token for a user and returns its secret. scope,
// when set, makes it an assistant token; a zero expires never expires.
func (d *Directory) CreateToken(user, name string, scope *Scope, expires time.Time) (string, Token, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return "", Token{}, errors.New("a token needs a name of up to 100 characters")
	}
	if _, ok := d.Get(user); !ok {
		return "", Token{}, ErrNotFound
	}
	var scopeJSON sql.NullString
	if scope != nil {
		if err := scope.Check(); err != nil {
			return "", Token{}, err
		}
		b, _ := json.Marshal(scope)
		scopeJSON = sql.NullString{String: string(b), Valid: true}
	}
	if !expires.IsZero() && !expires.After(d.now()) {
		return "", Token{}, errors.New("a token must expire in the future")
	}
	sec := TokenPrefix + secret(24)
	t := Token{ID: secret(6), Name: name, User: user, Created: d.now(), Expires: expires, Scope: scope}
	_, err := d.db.Exec(`INSERT INTO tokens (id, hash, user, name, created, scope, expires) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, hash256(sec), user, name, ms(t.Created), scopeJSON, nullMS(expires))
	return sec, t, err
}

func nullMS(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: ms(t), Valid: true}
}

// scanToken fills in a token's optional columns.
func scanToken(t *Token, created int64, used, expires sql.NullInt64, scope sql.NullString) {
	t.Created, t.LastUsed, t.Expires = at(created), atNull(used), atNull(expires)
	if scope.Valid {
		t.Scope = &Scope{}
		if json.Unmarshal([]byte(scope.String), t.Scope) != nil {
			// A scope that can't be read allows nothing, rather than everything.
			t.Scope = &Scope{}
		}
	}
}

// Tokens lists a user's API tokens.
func (d *Directory) Tokens(user string) []Token {
	out := []Token{}
	rows, err := d.db.Query(`SELECT id, name, created, last_used, expires, scope FROM tokens WHERE user = ? ORDER BY created`, user)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		t := Token{User: user}
		var created int64
		var used, expires sql.NullInt64
		var scope sql.NullString
		if rows.Scan(&t.ID, &t.Name, &created, &used, &expires, &scope) != nil {
			return out
		}
		scanToken(&t, created, used, expires, scope)
		out = append(out, t)
	}
	return out
}

// DeleteToken removes one of a user's API tokens.
func (d *Directory) DeleteToken(user, id string) error {
	res, err := d.db.Exec(`DELETE FROM tokens WHERE user = ? AND id = ?`, user, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TokenUser returns the user an API token belongs to, noting when it was used.
func (d *Directory) TokenUser(sec string) (User, bool) {
	u, _, ok := d.TokenAuth(sec)
	return u, ok
}

// TokenAuth returns an API token and its user, noting when it was used. An
// expired token signs nobody in. The lookup is by the secret's hash, so its
// timing reveals nothing about the secret.
func (d *Directory) TokenAuth(sec string) (User, Token, bool) {
	now := d.now()
	h := hash256(sec)
	var t Token
	var created int64
	var used, expires sql.NullInt64
	var scope sql.NullString
	err := d.db.QueryRow(`SELECT id, user, name, created, last_used, expires, scope FROM tokens WHERE hash = ?`, h).
		Scan(&t.ID, &t.User, &t.Name, &created, &used, &expires, &scope)
	if err != nil {
		return User{}, Token{}, false
	}
	scanToken(&t, created, used, expires, scope)
	if t.Expired(now) {
		return User{}, Token{}, false
	}
	if now.Sub(t.LastUsed) > seenEvery {
		_, _ = d.db.Exec(`UPDATE tokens SET last_used = ? WHERE hash = ?`, ms(now), h)
	}
	u, ok := d.Get(t.User)
	return u, t, ok
}

// Teams returns every team, by name.
func (d *Directory) Teams() []Team {
	out := []Team{}
	rows, err := d.db.Query(`SELECT name, notify FROM teams ORDER BY name`)
	if err != nil {
		return out
	}
	for rows.Next() {
		var t Team
		var notify string
		if rows.Scan(&t.Name, &notify) != nil {
			break
		}
		_ = json.Unmarshal([]byte(notify), &t.Notify)
		out = append(out, t)
	}
	_ = rows.Close()
	for i := range out {
		out[i].Members = d.members(out[i].Name)
	}
	return out
}

func (d *Directory) members(team string) []string {
	out := []string{}
	rows, err := d.db.Query(`SELECT user FROM team_members WHERE team = ? ORDER BY user`, team)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		if rows.Scan(&m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// SaveTeam creates or replaces a team.
func (d *Directory) SaveTeam(t Team) error {
	if err := CheckName(t.Name); err != nil {
		return err
	}
	slices.Sort(t.Members)
	t.Members = slices.Compact(t.Members)
	notify, err := json.Marshal(nonNil(t.Notify))
	if err != nil {
		return err
	}
	return d.tx(func(tx *sql.Tx) error {
		if user, err := exists(tx, `SELECT 1 FROM users WHERE name = ?`, t.Name); err != nil || user {
			return cmp(err, ErrExists)
		}
		for _, m := range t.Members {
			if ok, err := exists(tx, `SELECT 1 FROM users WHERE name = ?`, m); err != nil || !ok {
				return cmp(err, fmt.Errorf("no user %q", m))
			}
		}
		if _, err := tx.Exec(`INSERT INTO teams (name, notify, created) VALUES (?, ?, ?)
			ON CONFLICT (name) DO UPDATE SET notify = excluded.notify`, t.Name, string(notify), ms(d.now())); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM team_members WHERE team = ?`, t.Name); err != nil {
			return err
		}
		for _, m := range t.Members {
			if _, err := tx.Exec(`INSERT INTO team_members (team, user) VALUES (?, ?)`, t.Name, m); err != nil {
				return err
			}
		}
		return nil
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// DeleteTeam removes a team.
func (d *Directory) DeleteTeam(name string) error {
	res, err := d.db.Exec(`DELETE FROM teams WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Person implements alerts.Directory.
func (d *Directory) Person(name string) ([]alerts.Contact, bool) {
	u, ok := d.Get(name)
	return u.Contacts, ok
}

// Team implements alerts.Directory.
func (d *Directory) Team(name string) (members, notify []string, ok bool) {
	var raw string
	if err := d.db.QueryRow(`SELECT notify FROM teams WHERE name = ?`, name).Scan(&raw); err != nil {
		return nil, nil, false
	}
	_ = json.Unmarshal([]byte(raw), &notify)
	if len(notify) == 0 {
		notify = nil
	}
	return d.members(name), notify, true
}

// Names implements alerts.Directory.
func (d *Directory) Names() (people, teams []string) {
	return d.names(`SELECT name FROM users ORDER BY name`), d.names(`SELECT name FROM teams ORDER BY name`)
}

func (d *Directory) names(query string, args ...any) []string {
	var out []string
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	return out
}

// TeamsOf lists the teams a user is in.
func (d *Directory) TeamsOf(name string) []string {
	out := d.names(`SELECT team FROM team_members WHERE user = ? ORDER BY team`, name)
	if out == nil {
		out = []string{}
	}
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
