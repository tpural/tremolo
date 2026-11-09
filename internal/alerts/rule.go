// Package alerts runs alert rules written in Starlark, a dialect of Python,
// and notifies people when they fire and resolve.
package alerts

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing/fstest"
	"time"

	"go.starlark.net/lib/math"
	sltime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

//go:embed builtin/*.star
var builtinFS embed.FS

// Where a rule comes from. Built-in and file rules are read-only in the
// editor; editor rules are kept in the database, with every version.
const (
	FromBuiltin = "builtin"
	FromFile    = "file"
	FromEditor  = "editor"
)

// Rule is a Starlark file with a check(now) function.
type Rule struct {
	Name    string        `json:"name"`
	Builtin bool          `json:"builtin"`
	Origin  string        `json:"origin"`
	Version int           `json:"version,omitempty"`
	Every   time.Duration `json:"every"`
	For     time.Duration `json:"for"`
	Source  string        `json:"source"`
	// fsys holds the files the rule may load().
	fsys fs.FS
	file string
}

// ParseRule checks a rule's source and reads its EVERY and FOR. It may load()
// files from fsys, e.g. the rules directory's lib/.
func ParseRule(name, source string, fsys fs.FS) (*Rule, error) {
	if fsys == nil {
		fsys = fstest.MapFS{}
	}
	r := &Rule{Name: name, Origin: FromEditor, Source: source, fsys: fsys, file: name + ".star"}
	return r, r.parse()
}

const (
	defaultEvery = time.Minute
	minEvery     = 10 * time.Second
	// maxSteps bounds a run, so a runaway loop can't stall the engine.
	maxSteps = 5_000_000
	runFor   = 30 * time.Second
)

var fileOptions = &syntax.FileOptions{Set: true, While: true, TopLevelControl: true, GlobalReassign: true, Recursion: true}

// LoadRules returns the built-in rules, then the editor's, then the .star files
// at the top of dir; each replaces earlier ones with the same name. Files in
// subdirectories, e.g. lib/, are only for load(). Rules named in disable are
// left out.
func LoadRules(dir string, disable []string, editor ...*Rule) ([]*Rule, error) {
	byName := map[string]*Rule{}
	sub, err := fs.Sub(builtinFS, "builtin")
	if err != nil {
		return nil, err
	}
	if err := addRules(byName, sub, true); err != nil {
		return nil, err
	}
	for _, r := range editor {
		c := *r
		c.fsys, c.file = rulesFS(dir), r.Name+".star"
		byName[r.Name] = &c
	}
	if dir != "" {
		if err := addRules(byName, os.DirFS(dir), false); err != nil {
			return nil, err
		}
	}
	var errs []error
	out := make([]*Rule, 0, len(byName))
	for name, r := range byName {
		if slices.Contains(disable, name) {
			continue
		}
		if err := r.parse(); err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b *Rule) int { return strings.Compare(a.Name, b.Name) })
	return out, errors.Join(errs...)
}

func addRules(byName map[string]*Rule, fsys fs.FS, builtin bool) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return err
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".star")
		// Kubernetes ConfigMap mounts add hidden ..data entries.
		if !ok || e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return err
		}
		origin := FromFile
		if builtin {
			origin = FromBuiltin
		}
		byName[name] = &Rule{Name: name, Builtin: builtin, Origin: origin, Source: string(b), fsys: fsys, file: e.Name()}
	}
	return nil
}

// rulesFS is what editor rules may load(): the rules directory, if any.
func rulesFS(dir string) fs.FS {
	if dir == "" {
		return fstest.MapFS{}
	}
	return os.DirFS(dir)
}

// parse runs the file once with no data, to check it and read EVERY and FOR.
func (r *Rule) parse() error {
	globals, err := r.exec(context.Background(), &Env{})
	if err != nil {
		return err
	}
	if _, ok := globals["check"].(starlark.Callable); !ok {
		return fmt.Errorf("%s: no check(now) function", r.file)
	}
	r.Every, r.For = defaultEvery, 0
	for name, d := range map[string]*time.Duration{"EVERY": &r.Every, "FOR": &r.For} {
		v, ok := globals[name]
		if !ok {
			continue
		}
		s, ok := starlark.AsString(v)
		if !ok {
			return fmt.Errorf("%s: %s must be a duration string like \"5m\"", r.file, name)
		}
		if *d, err = time.ParseDuration(s); err != nil {
			return fmt.Errorf("%s: %s: %w", r.file, name, err)
		}
	}
	if r.Every < minEvery {
		return fmt.Errorf("%s: EVERY must be at least %s", r.file, minEvery)
	}
	return nil
}

// Found is one alert a rule returned.
type Found struct {
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	Severity string `json:"severity"`
	// Notify names channels or escalations; empty uses the severity's default.
	Notify []string          `json:"notify,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Severities, loudest first.
var Severities = []string{"page", "warn", "info"}

// Run calls check(now) and returns the alerts it found. Output from print()
// goes to env.Print.
func (r *Rule) Run(ctx context.Context, env *Env) ([]Found, error) {
	ctx, cancel := context.WithTimeout(ctx, runFor)
	defer cancel()
	env.ctx = ctx
	globals, err := r.exec(ctx, env)
	if err != nil {
		return nil, err
	}
	thread := r.thread(ctx, env)
	v, err := starlark.Call(thread, globals["check"], starlark.Tuple{nowValue(env.Now)}, nil)
	if err != nil {
		return nil, describe(err)
	}
	return found(v)
}

func (r *Rule) exec(ctx context.Context, env *Env) (starlark.StringDict, error) {
	globals, err := starlark.ExecFileOptions(fileOptions, r.thread(ctx, env), r.file, r.Source, env.predeclared())
	if err != nil {
		return nil, describe(err)
	}
	return globals, nil
}

func (r *Rule) thread(ctx context.Context, env *Env) *starlark.Thread {
	cache := map[string]starlark.StringDict{}
	thread := &starlark.Thread{
		Name: r.Name,
		Print: func(_ *starlark.Thread, msg string) {
			if env.Print != nil {
				env.Print(msg)
			}
		},
	}
	thread.Load = func(t *starlark.Thread, module string) (starlark.StringDict, error) {
		if g, ok := cache[module]; ok {
			return g, nil
		}
		name := path.Clean(module)
		if !fs.ValidPath(name) || !strings.HasSuffix(name, ".star") {
			return nil, fmt.Errorf("load %q: only .star files beside the rule", module)
		}
		b, err := fs.ReadFile(r.fsys, name)
		if err != nil {
			return nil, fmt.Errorf("load %q: %w", module, err)
		}
		g, err := starlark.ExecFileOptions(fileOptions, t, name, b, env.predeclared())
		cache[module] = g
		return g, err
	}
	thread.SetMaxExecutionSteps(maxSteps)
	thread.SetLocal("time.now", func() time.Time { return env.Now })
	context.AfterFunc(ctx, func() { thread.Cancel(ctx.Err().Error()) })
	return thread
}

// describe keeps the Starlark backtrace, which says where in the rule it failed.
func describe(err error) error {
	var e *starlark.EvalError
	if errors.As(err, &e) {
		return errors.New(e.Backtrace())
	}
	return err
}

// found turns check's return value into alerts: None, one alert, or a list.
func found(v starlark.Value) ([]Found, error) {
	var items []starlark.Value
	switch v := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case *starlarkstruct.Struct:
		items = []starlark.Value{v}
	case starlark.Indexable:
		for i := range v.Len() {
			items = append(items, v.Index(i))
		}
	default:
		return nil, fmt.Errorf("check returned %s; return ok(), alert(...) or a list of alerts", v.Type())
	}
	var out []Found
	keys := map[string]bool{}
	for _, item := range items {
		if item == starlark.None {
			continue
		}
		s, ok := item.(*starlarkstruct.Struct)
		if !ok || s.Constructor() != alertTag {
			return nil, fmt.Errorf("check returned %s in its list; only alerts", item.Type())
		}
		f := fromStruct(s)
		if keys[f.Key] {
			return nil, fmt.Errorf("two alerts with key %q; give each a different key=", f.Key)
		}
		keys[f.Key] = true
		out = append(out, f)
	}
	return out, nil
}

var alertTag = starlark.String("alert")

func alertBuiltin(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var summary string
	var notify starlark.Value = starlark.None
	severity, key := "warn", ""
	var labels []starlark.Tuple
	var known []starlark.Tuple
	for _, kv := range kwargs {
		switch string(kv[0].(starlark.String)) {
		case "summary", "severity", "notify", "key":
			known = append(known, kv)
		default:
			labels = append(labels, kv)
		}
	}
	if err := starlark.UnpackArgs("alert", args, known, "summary", &summary, "severity?", &severity, "notify?", &notify, "key?", &key); err != nil {
		return nil, err
	}
	if !slices.Contains(Severities, severity) {
		return nil, fmt.Errorf("alert: severity %q is not one of %s", severity, strings.Join(Severities, ", "))
	}
	targets, err := names(notify)
	if err != nil {
		return nil, fmt.Errorf("alert: notify %w", err)
	}
	d := starlark.StringDict{
		"summary": starlark.String(summary), "severity": starlark.String(severity),
		"notify": targets, "key": starlark.String(key),
	}
	ls := starlark.NewDict(len(labels))
	for _, kv := range labels {
		_ = ls.SetKey(kv[0], starlark.String(str(kv[1])))
	}
	d["labels"] = ls
	return starlarkstruct.FromStringDict(alertTag, d), nil
}

func fromStruct(s *starlarkstruct.Struct) Found {
	get := func(name string) string {
		v, _ := s.Attr(name)
		out, _ := starlark.AsString(v)
		return out
	}
	f := Found{Key: get("key"), Summary: get("summary"), Severity: get("severity")}
	if v, _ := s.Attr("notify"); v != nil {
		if l, ok := v.(*starlark.List); ok {
			for i := range l.Len() {
				n, _ := starlark.AsString(l.Index(i))
				f.Notify = append(f.Notify, n)
			}
		}
	}
	if v, _ := s.Attr("labels"); v != nil {
		if d, ok := v.(*starlark.Dict); ok && d.Len() > 0 {
			f.Labels = map[string]string{}
			for _, item := range d.Items() {
				k, _ := starlark.AsString(item[0])
				f.Labels[k] = str(item[1])
			}
		}
	}
	return f
}

// names reads notify=: None, one name, or a list of names.
func names(v starlark.Value) (*starlark.List, error) {
	if v == starlark.None {
		return starlark.NewList(nil), nil
	}
	if s, ok := starlark.AsString(v); ok {
		if s == "" {
			return starlark.NewList(nil), nil
		}
		return starlark.NewList([]starlark.Value{starlark.String(s)}), nil
	}
	seq, ok := v.(starlark.Indexable)
	if !ok {
		return nil, fmt.Errorf("must be a name or a list of names, not %s", v.Type())
	}
	out := make([]starlark.Value, 0, seq.Len())
	for i := range seq.Len() {
		s, ok := starlark.AsString(seq.Index(i))
		if !ok {
			return nil, fmt.Errorf("must list names, not %s", seq.Index(i).Type())
		}
		out = append(out, starlark.String(s))
	}
	return starlark.NewList(out), nil
}

// str shows a value the way a person would write it: strings without quotes.
func str(v starlark.Value) string {
	if s, ok := starlark.AsString(v); ok {
		return s
	}
	return v.String()
}

// nowValue is check's argument: the time in the configured zone, with the
// parts rules branch on.
func nowValue(t time.Time) starlark.Value {
	return starlarkstruct.FromStringDict(starlark.String("now"), starlark.StringDict{
		"year": starlark.MakeInt(t.Year()), "month": starlark.MakeInt(int(t.Month())), "day": starlark.MakeInt(t.Day()),
		"hour": starlark.MakeInt(t.Hour()), "minute": starlark.MakeInt(t.Minute()),
		"unix": starlark.MakeInt64(t.Unix()),
		"time": sltime.Time(t),
		// weekday() is Python's: Monday is 0.
		"weekday": starlark.NewBuiltin("weekday", func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
			return starlark.MakeInt((int(t.Weekday()) + 6) % 7), nil
		}),
		"date": starlark.String(t.Format(time.DateOnly)),
	})
}

var modules = starlark.StringDict{"time": sltime.Module, "math": math.Module}
