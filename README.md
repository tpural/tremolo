<p align="center"><img src="docs/logo.png" alt="tremolo" width="420"></p>

# tremolo

**Self-hosted observability with alerts as code.** tremolo is one server that accepts metrics, logs
and traces in the formats your tools already speak (OpenTelemetry, Prometheus remote write, Loki),
stores them in VictoriaMetrics, VictoriaLogs and VictoriaTraces, and gives you dashboards, log search,
traces and alerting in a fast web UI. Alert rules are real code, not a query and a threshold.

Send from servers, VMs, apps, CI jobs and Kubernetes clusters, any mix, to one place.

Named after the loon's tremolo call: the one it makes when something is wrong.

```
 apps and services  ── OTLP ────────────────────┐
 servers and VMs    ── OpenTelemetry Collector ─┤                ┌─▶ VictoriaMetrics
 Prometheus, Alloy  ── remote write ────────────┼──▶ tremolo ────┼─▶ VictoriaLogs
 Promtail           ── Loki push ───────────────┤       │        └─▶ VictoriaTraces
 Kubernetes         ── tremolo-kubernetes-collector     │
                                                        ├─▶ web UI: hosts, dashboards, logs, traces, alerts
                                                        └─▶ alerts: Slack, email, SMS, ntfy, Home Assistant, webhooks
```

## Why tremolo

- **Alerts as code.** Rules are [Starlark](https://github.com/google/starlark-go) (Python-like)
  programs: combine metrics, logs and host state, do any math, branch on the time of day, and pick who to
  tell. Escalation chains, teams, acknowledgement and silences are built in.
  ```python
  def check(now):
      errors = metric('sum(rate(http_requests_total{code=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))') or 0
      if errors > 0.05 and now.hour in range(9, 18):
          return alert("checkout failing", severity = "page", notify = "oncall")
      return ok()
  ```
- **Open standards in and out.** Anything that speaks OTLP (HTTP or gRPC), Prometheus remote write or the
  Loki push API can send. Underneath it's PromQL (MetricsQL), LogsQL and the Jaeger API, so Grafana or
  any other tool can read the same storage. No proprietary agent, no lock-in.
- **Metrics, logs and traces together.** Jump from a log line to its trace, from a span to the logs it
  wrote, from a host to everything it sent.
- **Triage together.** Users, teams and personal contacts: an alert reaches people their own way,
  and anyone can take it, forward it to a person or team with a note, or acknowledge it from the
  link on their phone. Every alert keeps a timeline.
- **Knows down from broken.** Hosts that are in maintenance, asleep or switched off on purpose are
  shown that way and don't page anyone. Machines that stop reporting are flagged, not forgotten.
- **Everything as code.** Dashboards are YAML, alert rules are Starlark files, config is one YAML file;
  keep them in git and deploy them with the rest of your infrastructure.
- **Efficient.** The Victoria databases are single binaries that use a fraction of the memory and disk
  of the usual stacks, so the whole thing runs on a small VM and grows from there.

## Run it

**Docker Compose**, on any machine:

```sh
git clone https://github.com/tpural/tremolo && cd tremolo/deploy/compose
docker compose up -d                                              # server and storage
HOST_NAME=$(hostname) docker compose --profile collector up -d    # also collect this machine
```

Open http://localhost:8080, sign in as `admin` with the password printed in
`docker compose logs tremolo`, and point your senders at port 4318 (HTTP) or 4317 (gRPC).

**Kubernetes**, with Helm:

```sh
helm install tremolo oci://ghcr.io/tpural/charts/tremolo -n tremolo --create-namespace
```

This installs the server, storage and **tremolo-kubernetes-collector**, which sends the cluster's nodes,
pods, container logs, events and annotated `/metrics` endpoints. To report more clusters to the same
tremolo, install only the collector there:

```sh
helm install tremolo-collector oci://ghcr.io/tpural/charts/tremolo-kubernetes-collector \
  -n tremolo --create-namespace --set tremolo.url=http://tremolo.example.com:4318 --set tremolo.token=...
```

**Anywhere else:** tremolo is one binary (`ghcr.io/tpural/tremolo`) that needs only the URLs of its three
databases. See [Configuration](docs/configuration.md).

## The UI

- **Hosts:** every machine with its state and CPU/memory, grouped into servers and Kubernetes clusters,
  plus charts across hosts. A quiet loon means nothing needs attention; a calling one lists what does.
- **Host pages:** CPU, memory, disks, network, filesystems and logs for one machine.
- **Dashboards:** built-in and your own, grouped in the sidebar.
- **Logs:** LogsQL search with namespace and level filters, field details, and live tail. A line with a
  trace ID links to its trace.
- **Alerts:** what's firing and pending (all, or yours) with each escalation's progress; acknowledge,
  take, forward, silence and add notes, with a timeline per alert; every rule with its source and a
  **Run now** button; and the history of past alerts.
- **Users and teams:** your profile, contacts and API tokens; for admins, users, roles and teams.
- **Traces:** each service's requests, error rate and p95; a search by service, operation, duration,
  failures or any span attribute, with a duration scatter; and a waterfall per trace with span details,
  events, and the logs written while it ran.

Every page has a time range (15m to 30d), refreshes on its own, and follows your light or dark theme.

## Documentation

- [Sending data](docs/sending-data.md): protocols, apps, servers, scripts, Kubernetes, and tokens
- [Alerts](docs/alerts.md): writing rules, notification channels, escalations and triage
- [Users and teams](docs/users.md): signing in, roles, contacts, teams and API tokens
- [Dashboards](docs/dashboards.md): dashboards as YAML
- [Hosts](docs/hosts.md): servers and nodes, host states, maintenance windows and providers
- [Configuration](docs/configuration.md): flags, Helm charts, storage, security and the HTTP API

## Development

```sh
make test     # Go and web tests
make lint     # golangci-lint and helm lint
make build    # builds the web app and embeds it in bin/tremolo

# Locally: docker compose up victoriametrics victorialogs (expose 8428/9428), then
make dev                    # API on :8080
cd web && npm run dev       # UI with hot reload on :5173
```

PR titles are conventional commits. Merging to `main` releases automatically. Comment `/rc` on a PR to
build a release-candidate image and chart.

## License

Apache 2.0
