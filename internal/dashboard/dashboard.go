// Package dashboard loads dashboards written as YAML: built-in ones, plus any in
// a directory, which replace built-ins with the same id.
package dashboard

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

//go:embed builtin/*.yaml
var builtin embed.FS

// Dashboard is a page of panels.
type Dashboard struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Group places the dashboard in the navigation, e.g. "Services".
	Group string `json:"group,omitempty"`
	// Variables are filled in by the page, e.g. $host on a host page.
	Variables []string `json:"variables,omitempty"`
	// Hidden dashboards are used by built-in pages and left out of the navigation.
	Hidden bool    `json:"hidden,omitempty"`
	Panels []Panel `json:"panels"`
}

// Panel is one chart, number, table or log view.
type Panel struct {
	Title string `json:"title"`
	// Type is timeseries, stat, table or logs.
	Type string `json:"type"`
	// Query is MetricsQL (PromQL) for metric panels and LogsQL for logs panels.
	Query string `json:"query"`
	// Legend names each series, e.g. "{{pod}}".
	Legend string `json:"legend,omitempty"`
	// Unit formats values: short, percent, percentunit, bytes, bytes/s, seconds, ms.
	Unit string `json:"unit,omitempty"`
	// Width is the share of a row out of 12.
	Width int `json:"width,omitempty"`
	// Stack stacks timeseries areas.
	Stack bool `json:"stack,omitempty"`
	// Thresholds color stat panels: warn at or above Warn, bad at or above Bad.
	Warn *float64 `json:"warn,omitempty"`
	Bad  *float64 `json:"bad,omitempty"`
}

var panelTypes = []string{"timeseries", "stat", "table", "logs"}

// Store holds dashboards by id.
type Store struct {
	list []Dashboard
	byID map[string]Dashboard
}

// Load reads the built-in dashboards and then dir, if set.
func Load(dir string) (*Store, error) {
	s := &Store{byID: map[string]Dashboard{}}
	if err := s.readFS(builtin, "builtin"); err != nil {
		return nil, err
	}
	if dir != "" {
		if err := s.readFS(os.DirFS(dir), "."); err != nil {
			return nil, err
		}
	}
	for _, d := range s.byID {
		s.list = append(s.list, d)
	}
	slices.SortFunc(s.list, func(a, b Dashboard) int {
		if c := strings.Compare(a.Group, b.Group); c != 0 {
			return c
		}
		return strings.Compare(a.Title, b.Title)
	})
	return s, nil
}

func (s *Store) readFS(fsys fs.FS, root string) error {
	var errs []error
	err := fs.WalkDir(fsys, root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// ConfigMap volumes keep the real files in hidden ..data directories.
		hidden := path != root && strings.HasPrefix(e.Name(), ".")
		if e.IsDir() {
			if hidden {
				return fs.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); hidden || (ext != ".yaml" && ext != ".yml") {
			return nil
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		d, err := Parse(b)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			return nil
		}
		s.byID[d.ID] = d
		return nil
	})
	return errors.Join(append(errs, err)...)
}

// Parse reads and validates one dashboard.
func Parse(b []byte) (Dashboard, error) {
	var d Dashboard
	if err := yaml.UnmarshalStrict(b, &d); err != nil {
		return d, err
	}
	var errs []error
	if d.ID == "" || d.Title == "" {
		errs = append(errs, errors.New("id and title are required"))
	}
	if len(d.Panels) == 0 {
		errs = append(errs, errors.New("no panels"))
	}
	for i := range d.Panels {
		p := &d.Panels[i]
		if p.Type == "" {
			p.Type = "timeseries"
		}
		if !slices.Contains(panelTypes, p.Type) {
			errs = append(errs, fmt.Errorf("panel %q: type must be one of %v", p.Title, panelTypes))
		}
		if p.Query == "" {
			errs = append(errs, fmt.Errorf("panel %q: no query", p.Title))
		}
		if p.Width == 0 {
			p.Width = 6
		}
		if p.Width < 1 || p.Width > 12 {
			errs = append(errs, fmt.Errorf("panel %q: width must be 1-12", p.Title))
		}
	}
	return d, errors.Join(errs...)
}

// List returns every dashboard, by group and title.
func (s *Store) List() []Dashboard { return s.list }

// Get returns a dashboard by id.
func (s *Store) Get(id string) (Dashboard, bool) {
	d, ok := s.byID[id]
	return d, ok
}
