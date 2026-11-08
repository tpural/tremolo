// Command tremolo is an observability server: it accepts pushed metrics and
// logs, stores them in VictoriaMetrics and VictoriaLogs, and serves dashboards
// and log search.
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
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/tpural/tremolo/internal/api"
	"github.com/tpural/tremolo/internal/cluster"
	"github.com/tpural/tremolo/internal/dashboard"
	"github.com/tpural/tremolo/internal/hosts"
	"github.com/tpural/tremolo/internal/ingest"
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
	possumURL := fset.String("possum-url", "", "possum status URL, to show nodes it has switched off (optional)")
	dashboards := fset.String("dashboards", "", "directory of extra dashboards (YAML); they replace built-ins with the same id")
	hostLookback := fset.Duration("host-lookback", 30*24*time.Hour, "how long a host that stopped sending metrics stays listed (as unreachable); match metrics retention")
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
		cl = cluster.New(kube, *possumURL, &http.Client{Transport: transport, Timeout: 5 * time.Second})
	}
	opts := api.Options{
		MetricsURL: mURL, LogsURL: lURL, Dashboards: store, UI: ui,
		Hosts:     hosts.New(mURL, &http.Client{Transport: transport}, cl, *hostLookback),
		Transport: transport, Version: version, Log: log,
	}
	gw := ingest.New(ingest.Options{MetricsURL: mURL, LogsURL: lURL, Tokens: tokens, Transport: transport, Log: log})
	log.Info("starting", "version", version, "listen", *listen, "ingest", *ingestListen, "grpc", *grpcListen,
		"tokens", len(tokens), "metrics", mURL, "logs", lURL, "dashboards", len(store.List()), "possum", *possumURL)

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

func parseURL(name, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("%s %q is not an http(s) URL", name, raw)
	}
	return u, nil
}
