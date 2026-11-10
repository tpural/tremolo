// Command tremolo is an observability server: it accepts pushed metrics, logs
// and traces, stores them in VictoriaMetrics, VictoriaLogs and VictoriaTraces,
// and serves dashboards, log search and trace views.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// Distroless images have no zone database; rules need the configured zone.
	_ "time/tzdata"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/tpural/tremolo/internal/alerts"
	"github.com/tpural/tremolo/internal/api"
	"github.com/tpural/tremolo/internal/calendar"
	"github.com/tpural/tremolo/internal/cluster"
	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/heartbeat"
	"github.com/tpural/tremolo/internal/hosts"
	"github.com/tpural/tremolo/internal/ingest"
	"github.com/tpural/tremolo/internal/runbooks"
	"github.com/tpural/tremolo/internal/state"
	"github.com/tpural/tremolo/internal/storage"
	"github.com/tpural/tremolo/internal/users"
	"github.com/tpural/tremolo/web"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		slog.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fset := flag.NewFlagSet("tremolo", flag.ExitOnError)
	listen := fset.String("listen", ":8080", "address to serve the UI and API on")
	ingestListen := fset.String("ingest-listen", ":4318", "address for OTLP/HTTP, Prometheus remote write and Loki push (empty to disable)")
	grpcListen := fset.String("grpc-listen", ":4317", "address for OTLP/gRPC (empty to disable)")
	tokensFile := fset.String("ingest-tokens-file", "", "YAML map of sender name to bearer token; without it, ingest is open")
	metricsURL := fset.String("metrics-url", "http://localhost:8428", "VictoriaMetrics URL")
	logsURL := fset.String("logs-url", "http://localhost:9428", "VictoriaLogs URL")
	tracesURL := fset.String("traces-url", "", "VictoriaTraces URL; empty turns traces off")
	possumURL := fset.String("possum-url", "", "possum status URL; turns on the possum provider for nodes it suspends or switches off")
	asleepTaints := fset.String("asleep-taints", "", "comma-separated node taints that mean a node is asleep on purpose, e.g. possum.dev/asleep")
	rulesDir := fset.String("rules-dir", "", "directory of alert rules (.star files); they replace built-ins with the same name")
	alertsConfig := fset.String("alerts-config", "", "alerts config file (YAML): timezone, channels, which channel each severity goes to")
	dataDir := fset.String("data-dir", "", "directory for tremolo's database (users, alerts, history, maintenance); empty keeps it in memory")
	runbooksDir := fset.String("runbooks-dir", "", "directory of runbooks (.md) added on start when missing; after that they're edited in tremolo")
	dashboards := fset.String("dashboards", "", "directory of extra dashboards (YAML); they replace built-ins with the same id")
	resetAdmin := fset.Bool("reset-admin", false, "if every admin is locked out: make the user admin an admin again, with the password in $TREMOLO_ADMIN_PASSWORD")
	historyRetention := fset.Duration("history-retention", 365*24*time.Hour, "how long resolved alerts and the audit log are kept")
	hostLookback := fset.Duration("host-lookback", 30*24*time.Hour, "how long a host that stopped sending metrics stays listed (as unreachable); match metrics retention")
	heartbeatURL := fset.String("heartbeat-url", "", "URL to GET while alerting works, for a watcher such as Healthchecks.io; ${VAR}s are read from the environment")
	heartbeatEvery := fset.Duration("heartbeat-every", time.Minute, "how often to check alerting and ping --heartbeat-url")
	backupDir := fset.String("backup-dir", "", "directory for copies of the database, made while tremolo runs; empty turns backups off")
	backupEvery := fset.Duration("backup-every", 24*time.Hour, "how often to back up the database")
	backupKeep := fset.Int("backup-keep", 7, "how many backups to keep")
	showVersion := fset.Bool("version", false, "print the version and exit")
	_ = fset.Parse(args)
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	mURL, err := parseURL("metrics-url", *metricsURL)
	if err != nil {
		return err
	}
	lURL, err := parseURL("logs-url", *logsURL)
	if err != nil {
		return err
	}
	var tURL *url.URL
	if *tracesURL != "" {
		if tURL, err = parseURL("traces-url", *tracesURL); err != nil {
			return err
		}
	}
	store, err := dashboard.Load(*dashboards)
	if err != nil {
		return fmt.Errorf("dashboards: %w", err)
	}
	ui, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
	}
	var tokens map[string]string
	if *tokensFile != "" {
		if tokens, err = ingest.LoadTokens(*tokensFile); err != nil {
			return err
		}
	}
	// Kubernetes access is optional: without it, hosts come from metrics alone.
	var cl *cluster.Cluster
	if cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), nil).ClientConfig(); err != nil {
		log.Warn("no Kubernetes access; node states are unavailable", "err", err)
	} else {
		kube, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return err
		}
		cl = cluster.New(kube, splitList(*asleepTaints))
	}
	db, err := state.Open(*dataDir)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()
	go prune(ctx, db, *historyRetention, log)
	if *backupDir != "" {
		if *dataDir == "" {
			return errors.New("--backup-dir needs --data-dir: an in-memory database has nothing to back up")
		}
		go db.RunBackups(ctx, state.BackupOptions{
			Dir: *backupDir, Keep: *backupKeep, Every: *backupEvery,
			MetricsURL: mURL, Client: &http.Client{Transport: transport, Timeout: 30 * time.Second},
			Log: log.With("component", "backup"),
		})
	}
	notifyClient := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	alertsCfg, err := alerts.LoadConfig(*alertsConfig, notifyClient)
	if err != nil {
		return fmt.Errorf("alerts config: %w", err)
	}
	// On-call schedules and maintenance periods; the alerts timezone is theirs.
	cal, err := calendar.Open(db, alertsCfg.Location())
	if err != nil {
		return fmt.Errorf("calendar: %w", err)
	}
	maintenance := hosts.NewMaintenance(cal)
	books, err := runbooks.Open(db)
	if err != nil {
		return fmt.Errorf("runbooks: %w", err)
	}
	if *runbooksDir != "" {
		if n, err := books.Import(ctx, *runbooksDir); err != nil {
			log.Warn("importing runbooks", "err", err)
		} else if n > 0 {
			log.Info("imported runbooks", "count", n, "dir", *runbooksDir)
		}
	}
	// Later providers win: maintenance is set by a person, so it goes last.
	var providers []hosts.Provider
	if *possumURL != "" {
		providers = append(providers, hosts.NewPossum(*possumURL, &http.Client{Transport: transport, Timeout: 5 * time.Second}))
	}
	providers = append(providers, maintenance)
	dir, err := users.Open(db)
	if err != nil {
		return fmt.Errorf("users: %w", err)
	}
	// With $TREMOLO_ADMIN_PASSWORD the first start makes an admin; without it,
	// the first person to open tremolo sets one up.
	if *resetAdmin {
		if err := dir.ResetAdmin(os.Getenv("TREMOLO_ADMIN_PASSWORD")); err != nil {
			return fmt.Errorf("resetting admin: %w", err)
		}
		_ = db.Audit(ctx, state.Entry{Actor: "tremolo", Action: "reset the admin account", Target: "admin", Detail: "--reset-admin"})
		log.Warn("reset the admin account to $TREMOLO_ADMIN_PASSWORD; sign in and remove --reset-admin", "user", "admin")
	} else if err := dir.Bootstrap(os.Getenv("TREMOLO_ADMIN_PASSWORD")); err != nil {
		return fmt.Errorf("creating the first admin: %w", err)
	}
	if dir.Empty() {
		log.Warn("no users yet: open tremolo in a browser to create the admin account")
	}
	if *dataDir == "" {
		log.Warn("no --data-dir: users, alerts and maintenance are lost on restart")
	}
	hostList := hosts.New(mURL, &http.Client{Transport: transport}, cl, *hostLookback, providers...)
	// Host states go to the metrics and logs stores as they're seen, so they
	// can be charted, searched, and replayed by alert rules.
	hostHistory := hosts.NewHistory(hostList, mURL, lURL, &http.Client{Transport: transport, Timeout: 30 * time.Second}, log.With("component", "hosts"))
	go hostHistory.Run(ctx, 30*time.Second)
	engine, err := alerts.New(alerts.Options{
		Config:    alertsCfg,
		Data:      &alerts.Data{MetricsURL: mURL, LogsURL: lURL, Hosts: hostList, HostHistory: hostHistory, Client: &http.Client{Transport: transport}},
		DB:        db,
		Directory: dir,
		Calendar:  cal,
		Runbooks:  books,
		Client:    notifyClient,
		Log:       log.With("component", "alerts"),
	})
	if err != nil {
		return fmt.Errorf("alerts: %w", err)
	}
	go engine.Watch(ctx, *rulesDir, 30*time.Second)
	go engine.Export(ctx, 30*time.Second)
	go engine.RunDigest(ctx)
	var beat *heartbeat.Heartbeat
	if *heartbeatURL != "" {
		u := os.ExpandEnv(*heartbeatURL)
		if u == "" {
			return fmt.Errorf("--heartbeat-url %q is empty once its variables are read", *heartbeatURL)
		}
		client := &http.Client{Transport: transport}
		beat = heartbeat.New(heartbeat.Options{
			URL:        u,
			Client:     client,
			MetricsURL: mURL,
			Log:        log.With("component", "heartbeat"),
			Checks: []heartbeat.Check{
				{Name: "rules", Run: func(context.Context) error { return engine.Healthy() }},
				{Name: "metrics", Run: heartbeat.Health(client, mURL)},
				{Name: "logs", Run: heartbeat.Health(client, lURL)},
				{Name: "database", Run: db.Check},
			},
		})
		go beat.Run(ctx, *heartbeatEvery)
	}
	gw := ingest.New(ingest.Options{MetricsURL: mURL, LogsURL: lURL, TracesURL: tURL, Tokens: tokens, Transport: transport, Log: log})
	sampler := &storage.Sampler{
		Stores: storage.Stores(mURL, lURL, tURL), Counts: gw.Counts, MetricsURL: mURL,
		Client: &http.Client{Transport: transport, Timeout: 30 * time.Second}, Log: log.With("component", "storage"),
	}
	go sampler.Run(ctx, time.Minute)
	opts := api.Options{
		MetricsURL: mURL, LogsURL: lURL, TracesURL: tURL, Dashboards: store, UI: ui,
		Hosts:       hostList,
		HostHistory: hostHistory,
		Maintenance: maintenance,
		Calendar:    cal,
		Alerts:      engine,
		Heartbeat:   beat,
		Runbooks:    books,
		Storage:     sampler,
		Users:       dir,
		DB:          db,
		Transport:   transport, Version: version, Log: log,
	}
	log.Info("starting", "version", version, "listen", *listen, "ingest", *ingestListen, "grpc", *grpcListen,
		"tokens", len(tokens), "metrics", mURL, "logs", lURL, "traces", *tracesURL, "dashboards", len(store.List()), "providers", providerNames(providers), "data", *dataDir, "heartbeat", beat != nil, "backups", *backupDir)

	// No write timeout on the UI: log tails stream for as long as the browser stays.
	servers := []*http.Server{{Addr: *listen, Handler: api.New(opts), ReadHeaderTimeout: 10 * time.Second}}
	if *ingestListen != "" {
		servers = append(servers, &http.Server{Addr: *ingestListen, Handler: gw.Handler(), ReadHeaderTimeout: 10 * time.Second})
	}
	errc := make(chan error, len(servers)+1)
	for _, srv := range servers {
		go func() {
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}
	grpcSrv := gw.GRPCServer()
	if *grpcListen != "" {
		lis, err := net.Listen("tcp", *grpcListen)
		if err != nil {
			return err
		}
		go func() { errc <- grpcSrv.Serve(lis) }()
	}
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(shutdownCtx)
	}
	grpcSrv.GracefulStop()
	return err
}

func splitList(s string) []string {
	var out []string
	for f := range strings.SplitSeq(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func providerNames(ps []hosts.Provider) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name()
	}
	return names
}

func parseURL(name, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%s %q is not an http(s) URL", name, raw)
	}
	return u, nil
}

// prune drops old alert history and audit entries once a day.
func prune(ctx context.Context, db *state.DB, keep time.Duration, log *slog.Logger) {
	for {
		if n, err := db.Prune(ctx, keep); err != nil {
			log.Warn("pruning history", "err", err)
		} else if n > 0 {
			log.Info("pruned history", "rows", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}
