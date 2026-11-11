package frontend

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestApps(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ms := func(ago time.Duration) int64 { return now.Add(-ago).UnixMilli() }
	var filtered []string
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filtered = append(filtered, r.URL.Query().Get("extra_filters[]"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/values"):
			fmt.Fprint(w, `{"data":["shop"]}`)
		case r.URL.Query().Get("match[]") == "browser_events":
			fmt.Fprintf(w, `{"metric":{"app":"shop","kind":"page_view","page":"/"},"values":[1,1,1],"timestamps":[%d,%d,%d]}`+"\n",
				ms(2*24*time.Hour), ms(time.Hour), ms(time.Minute))
			fmt.Fprintf(w, `{"metric":{"app":"shop","kind":"page_view","page":"/admin"},"values":[1],"timestamps":[%d]}`+"\n", ms(time.Second))
			fmt.Fprintf(w, `{"metric":{"app":"shop","kind":"exception","page":"/"},"values":[1],"timestamps":[%d]}`+"\n", ms(time.Hour))
		default:
			fmt.Fprintf(w, `{"metric":{"app":"shop","name":"lcp","page":"/"},"values":[100,200,300,400,500],"timestamps":[1,2,3,4,5]}`+"\n")
			fmt.Fprintf(w, `{"metric":{"app":"shop","name":"lcp","page":"/admin"},"values":[9000],"timestamps":[6]}`+"\n")
		}
	}))
	t.Cleanup(vm.Close)
	u, _ := url.Parse(vm.URL)
	src := &Source{MetricsURL: u, Client: http.DefaultClient, Config: Config{
		"shop": {IgnorePages: []string{"/admin"}},
		"blog": {},
	}}

	list, err := src.Apps(context.Background(), now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "blog" || list[1].Name != "shop" {
		t.Fatalf("apps = %+v", list)
	}
	shop := list[1]
	// Views today leave out the ignored page and the one from two days ago.
	if shop.Views != 2 || shop.Errors != 1 || !shop.LastSeen.Equal(time.UnixMilli(ms(time.Minute))) {
		t.Errorf("shop = %+v", shop)
	}
	if v := shop.Vitals["lcp"]; v.P75 != 400 || v.Samples != 5 {
		t.Errorf("lcp = %+v", v)
	}

	// A token's filter is applied, and only apps with data it may read are listed.
	filtered = nil
	list, _ = src.Apps(context.Background(), now, func(q url.Values) { q.Set("extra_filters[]", `{app="shop"}`) })
	if len(list) != 1 || list[0].Name != "shop" {
		t.Errorf("filtered apps = %+v", list)
	}
	for _, f := range filtered {
		if f != `{app="shop"}` {
			t.Errorf("unfiltered query: %q", filtered)
		}
	}
}

func TestDashboard(t *testing.T) {
	cfg := App{Namespace: "shop", IgnorePages: []string{"/admin", "/a.b"}}
	d := Dashboard("shop", cfg)
	if cfg.IgnorePages[0] != "/admin" {
		t.Error("changed the config")
	}
	var metrics, logs, server int
	for _, p := range d.Panels {
		switch {
		case strings.HasPrefix(p.Title, "Server"), strings.HasPrefix(p.Title, "Restarts"):
			server++
		case p.Type == "logs":
			logs++
			if !strings.Contains(p.Query, `-page:in("/admin", "/a.b")`) {
				t.Errorf("%s: %s", p.Title, p.Query)
			}
		default:
			metrics++
			if !strings.Contains(p.Query, `page!~"/admin|/a\\.b"`) {
				t.Errorf("%s: %s", p.Title, p.Query)
			}
		}
	}
	if metrics != 10 || logs != 1 || server != 4 {
		t.Errorf("panels: %d metrics, %d logs, %d server", metrics, logs, server)
	}
	if n := len(Dashboard("blog", App{}).Panels); n != 11 {
		t.Errorf("without a namespace: %d panels", n)
	}
	if _, err := ParseConfig(`{"bad name":{}}`); err == nil {
		t.Error("accepted a bad name")
	}
}

func TestQuantile(t *testing.T) {
	if q := quantile([]float64{4, 1, 3, 2}, 0.75); q != 3.25 {
		t.Errorf("p75 = %v", q)
	}
	if q := quantile([]float64{7}, 0.75); q != 7 {
		t.Errorf("one value = %v", q)
	}
}
