<p align="center"><img src="docs/logo.png" alt="tremolo" width="420"></p>

# tremolo

**Observability for small setups: push metrics, logs and traces, get good-looking dashboards.** tremolo
is a server that accepts the formats your tools already speak (OpenTelemetry, Prometheus remote write,
Loki), stores them in VictoriaMetrics, VictoriaLogs and VictoriaTraces, and serves a fast web UI for
hosts, dashboards, log search and traces. There is no Grafana to set up.

Named after the loon's tremolo call: the one it makes when something is wrong.

```
 apps, CI runners  ── OTLP ──────────────┐
 Linux machines    ── OpenTelemetry ─────┤
 Prometheus/Alloy  ── remote write ──────┼──▶ tremolo ──▶ VictoriaMetrics + VictoriaLogs + VictoriaTraces
 Promtail          ── Loki push ─────────┤        │
 Kubernetes        ── tremolo-kubernetes-collector       └──▶ web UI: hosts, dashboards, logs, traces
```

## Why

- **One server, many senders.** Anything that speaks OTLP (HTTP or gRPC), Prometheus remote write or
  the Loki push API can send to tremolo. A CI runner, a Raspberry Pi and a Kubernetes cluster can all
  report to the same place.
- **Light:** VictoriaMetrics, VictoriaLogs and VictoriaTraces are single binaries that need a fraction of the memory and
  disk of the usual stack.
- **Hosts first:** every machine that sends host metrics shows up with its CPU, memory, disks and logs,
  whether or not it is in a cluster. Machines that stop reporting are shown as unreachable.
- **Dashboards as code:** small YAML files. Add your own without touching the UI.
- **Knows down from broken:** hosts that are asleep, switched off or in maintenance on purpose are
  shown that way, not as failures. See [Host state providers](#host-state-providers).
- **Standard underneath:** metrics speak PromQL (MetricsQL), logs and spans speak LogsQL, and traces
  are also served through the Jaeger query API. Point Grafana or anything
  else at the same storage if you want to.

## Run it

### On one machine (Docker Compose)

```sh
git clone https://github.com/tpural/tremolo && cd tremolo/deploy/compose
docker compose up -d                                              # server and storage
HOST_NAME=$(hostname) docker compose --profile collector up -d    # also collect this machine
```

Open http://localhost:8080.

### On Kubernetes

```sh
helm install tremolo oci://ghcr.io/tpural/charts/tremolo -n tremolo --create-namespace
```

This installs the server, storage and **tremolo-kubernetes-collector**, which sends the cluster's
node and pod metrics, container logs, events and the `/metrics` of annotated pods to it. To report
another cluster to the same tremolo, install only the collector there:

```sh
helm install tremolo-collector oci://ghcr.io/tpural/charts/tremolo-kubernetes-collector \
  -n tremolo --create-namespace --set tremolo.url=http://tremolo.example.com:4318 --set tremolo.token=...
```

For a permanent UI address, enable the chart's Gateway API route (`httpRoute`).

**Traces** are off by default in the chart while VictoriaTraces is before 1.0. Turn them on with
`--set traces.enabled=true`; Docker Compose has them on.

## Sending data

| Protocol | Address | Path |
|---|---|---|
| OTLP/HTTP (protobuf or JSON) | `:4318` | `/v1/metrics`, `/v1/logs`, `/v1/traces` |
| OTLP/gRPC | `:4317` | metrics, logs and trace services |
| Prometheus remote write | `:4318` | `/api/v1/write` |
| Loki push | `:4318` | `/loki/api/v1/push` |

Without trace storage, trace exports get `501`/`Unimplemented`.

**From an app** using an OpenTelemetry SDK:

```sh
OTEL_EXPORTER_OTLP_ENDPOINT=http://tremolo:4318
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer <token>"
OTEL_RESOURCE_ATTRIBUTES=host.name=$(hostname)
```

**From a pod in a cluster with tremolo-kubernetes-collector:** send to the node's agent, which adds the
pod, namespace and node to everything:

```yaml
env:
  - name: NODE_IP
    valueFrom: {fieldRef: {fieldPath: status.hostIP}}
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: http://$(NODE_IP):4318
```

**From a Linux machine:** run the OpenTelemetry Collector (contrib) with
[`deploy/host/otelcol.yaml`](deploy/host/otelcol.yaml) and `TREMOLO_URL` / `TREMOLO_TOKEN` set.

**From a script or CI job:**

```sh
curl -X POST http://tremolo:4318/v1/logs -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"resourceLogs":[{"resource":{"attributes":[
    {"key":"service.name","value":{"stringValue":"nightly-build"}}]},
    "scopeLogs":[{"logRecords":[{"severityText":"ERROR","body":{"stringValue":"build failed"}}]}]}]}'
```

**From a Kubernetes pod:** annotate it and the collector scrapes its `/metrics`:

```yaml
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "9090"
    prometheus.io/path: /metrics   # optional
```

### Tokens

Without tokens, anyone who can reach ports 4317/4318 can write. Before exposing them, give each sender
a token:

```yaml
# tokens.yaml, passed with --ingest-tokens-file (chart: ingest.tokens or ingest.existingSecret)
ci: 6f1c…
laptop: 93ab…
```

Senders send `Authorization: Bearer <token>`. The token's name is added to everything it sends as a
`source` label (metrics) or field (logs), so you can tell senders apart.

### Kubernetes nodes and servers

tremolo shows each host as a **Kubernetes node** or a **server**, from how its metrics arrive:
tremolo-kubernetes-collector marks everything it sends with `tremolo.collector=kubernetes` and the
cluster's name (`clusterName` in its values), and the host collector config marks its machines with
`tremolo.collector=host`. Nodes are grouped by cluster, so several clusters can report to one tremolo.
Any other sender of host metrics shows up as a server.

## The UI

- **Hosts:** every machine with its state and CPU/memory, grouped into Kubernetes clusters and servers,
  plus charts across hosts. A quiet loon means nothing needs attention; a calling one lists what does.
- **Host pages:** CPU, memory, disks, network, filesystems and logs for one machine.
- **Dashboards:** built-in and your own, grouped in the sidebar.
- **Logs:** LogsQL search with namespace and level filters, field details, and live tail. A line with a
  trace ID links to its trace.
- **Traces:** each service's requests, error rate and p95; a search by service, operation, duration,
  failures or any span attribute, with a duration scatter; and a waterfall per trace with span details,
  events, and the logs written while it ran.

Every page has a time range (15m to 30d), refreshes on its own, and follows your light or dark theme.

## Dashboards

A dashboard is a YAML file of panels. Add your own through the chart:

```yaml
dashboards:
  my-app.yaml: |
    id: my-app
    title: My app
    group: Services
    panels:
      - title: Requests per second
        query: sum by (code) (rate(http_requests_total{namespace="my-app"}[5m]))
        legend: "{{code}}"
        width: 8
      - title: Error rate
        type: stat
        query: sum(rate(http_requests_total{code=~"5.."}[5m])) / sum(rate(http_requests_total[5m]))
        unit: percentunit
        warn: 0.01
        bad: 0.05
        width: 4
      - title: Errors
        type: logs
        query: 'k8s.namespace.name:"my-app" error'
        width: 12
```

| Panel key | Meaning |
|---|---|
| `type` | `timeseries` (default), `stat`, `table` or `logs` |
| `query` | MetricsQL/PromQL, or LogsQL for `logs` panels. Page variables such as `$host` are filled in. |
| `legend` | Series name template, e.g. `{{pod}}` |
| `unit` | `short`, `percent`, `percentunit`, `bytes`, `bytes/s`, `seconds`, `ms`, `celsius` |
| `width` | Columns out of 12 (default 6) |
| `stack` | Stack timeseries areas |
| `warn` / `bad` | Thresholds that color `stat` and `table` values |

A dashboard with the same `id` as a built-in replaces it.

## Alerts

Alert rules are code: small [Starlark](https://github.com/google/starlark-go) files, a dialect of
Python, each with a `check(now)` function. A rule can read metrics, logs and hosts, do any
calculation, branch on the time, and choose who to tell and how loudly.

```python
# gpu-busy.star
EVERY = "1m"   # how often it runs (default 1m)
FOR = "5m"     # how long it must hold before firing (default 0)

def check(now):
    queue = metric('sum(llamaswap_queue_depth)')
    temp = metric('max(dcgm_gpu_temp{host_name="gpu01"})')
    if queue == None or temp == None:
        return ok()
    if queue > 10 and temp > 80:
        if now.weekday() < 5 and now.hour in range(10, 17):
            return alert("gpu01 overloaded during work hours", severity = "page", notify = "phone")
        return alert("gpu01 overloaded", severity = "info", notify = "ntfy")
    return ok()
```

A rule returns `ok()`, one `alert(...)`, or a list of them, each with its own `key=` (one per host,
say). An alert keeps firing while the rule keeps returning it, and resolves when it stops.

| In a rule | |
|---|---|
| `metric(q)` | one number from a MetricsQL query, or `None` when nothing matches |
| `metrics(q)` | every series: a list with `.labels` (dict) and `.value` |
| `series(q, since="1h", step="1m")` | values over time: a list with `.labels`, `.values` and `.times` |
| `log_count(q, since="15m")` | how many log lines match a LogsQL query |
| `logs(q, since="15m", limit=100)` | matching log lines, newest first, as dicts |
| `hosts()` | every host by name, with `.state` (ready, notready, asleep, off, booting, maintenance, unreachable), `.state_by`, `.reason`, `.kind`, `.details` |
| `now` | the time in `timezone`: `.hour`, `.minute`, `.weekday()` (Monday is 0), `.date`, `.unix`, `.time` |
| `alert(summary, severity="warn", notify=None, key="", **labels)` | severity is `page`, `warn` or `info`; `notify` is a channel or escalation name, or a list, else the severity's default |
| `load("lib/util.star", "fn")` | shared helpers from files under the rules directory |
| `time`, `math`, `print` | Starlark's time and math modules; `print` shows in **Run now** |

Starlark is Python without imports, files or the network, and with no `a < b < c` chains
(`b in range(a, c)` works). A rule runs for at most 30 seconds.

**Built-in rules**, each replaceable by a file of the same name or turned off with `disable`:
`host-down` (a host not ready or not reporting, unless asleep, off or in maintenance), `crash-loop`
(a pod restarting 3+ times in 30 minutes) and `disk-full` (over 90%, or full within a day at its
current rate).

**Config** (`--alerts-config`, the chart's `alerts.config`); `${VAR}`s come from the environment:

```yaml
timezone: America/Chicago
url: https://tremolo.example.com        # links in notifications, including "acknowledge"
repeat: 4h                              # resend while firing and unacknowledged; 0s sends once
disable: [disk-full]
notify:                                 # where each severity goes when a rule doesn't say
  page: oncall                          # a channel, an escalation, or a list of them
  warn: [phone, slack]
channels:
  phone:
    type: homeassistant                 # Home Assistant's notify service, e.g. the companion app
    url: http://home-assistant:8123
    service: mobile_app_my_phone
    token: ${HA_TOKEN}
  slack:
    type: slack                         # an incoming webhook
    url: ${SLACK_WEBHOOK}
  mail:
    type: email                         # SMTP, with STARTTLS when offered
    host: smtp.example.com
    port: 587
    username: tremolo@example.com
    password: ${SMTP_PASSWORD}
    from: tremolo@example.com
    to: [me@example.com]
  sms:
    type: twilio
    accountSid: ${TWILIO_SID}
    authToken: ${TWILIO_TOKEN}
    from: "+15551234567"
    to: ["+15557654321"]
  topic:
    type: ntfy
    url: https://ntfy.sh/my-topic
  hook:
    type: webhook                       # POSTs the notification as JSON
    url: https://example.com/hook
escalations:
  oncall:                               # each step after the alert has fired unacknowledged this long
    - notify: mail
    - after: 15m
      notify: slack
    - after: 30m
      notify: [sms, phone]
```

**Acknowledging** an alert, from the Alerts page or the link in its notification, stops its
escalation and repeats and tells the channels already notified; it still sends "resolved" when it
ends. Links open a confirm button rather than acknowledging on their own, so chat link previews
can't. Providers live in `internal/alerts/notify`, one file each; a new one registers a type name
and reads its own settings.

The **Alerts** page shows what's firing and pending, lets you silence an alert or a whole rule, shows
each rule's source and last run, and runs a rule on demand without changing anything. Firing and
resolved events are written to the logs (`stream:="tremolo-alerts"`), so the history is searchable.

## Configuration

**Server flags:**

| Flag | Default | Meaning |
|---|---|---|
| `--listen` | `:8080` | UI and API |
| `--ingest-listen` | `:4318` | OTLP/HTTP, remote write, Loki push (empty disables) |
| `--grpc-listen` | `:4317` | OTLP/gRPC (empty disables) |
| `--ingest-tokens-file` | | YAML map of sender name to token |
| `--metrics-url` / `--logs-url` | `localhost:8428` / `:9428` | VictoriaMetrics and VictoriaLogs |
| `--traces-url` | | VictoriaTraces; empty turns traces off |
| `--dashboards` | | Directory of extra dashboards |
| `--host-lookback` | `720h` | How long a host that stopped sending metrics stays listed as unreachable; match metrics retention |
| `--data-dir` | | Directory for tremolo's own state, such as maintenance windows; empty keeps it in memory |
| `--asleep-taints` | | Comma-separated node taints that mean asleep on purpose |
| `--possum-url` | | Turns on the possum provider |
| `--rules-dir` | | Alert rules (`.star`); reloaded when they change |
| `--alerts-config` | | Alert timezone, channels and routing (YAML) |

Kubernetes access is optional and picked up automatically (in-cluster or `KUBECONFIG`); with it, hosts
also show their Kubernetes state.

### Host state providers

A host's state comes from Kubernetes (ready, not ready) or from its metrics (unreachable when they
stop). Providers add the cases that are on purpose, so they don't read as broken:

- **maintenance** (always on): from a host's page, or `PUT /api/hosts/{name}/maintenance` with
  `{"until": "<RFC 3339 time>", "note": "..."}` (omit `until` to keep it until cleared) and
  `DELETE` to end it. It wins over every other state.
- **kubernetes**: cordoned nodes are in maintenance; nodes with one of `--asleep-taints` are asleep.
- **possum** (`--possum-url`): nodes [possum](https://github.com/tpural/possum), a power autoscaler,
  has suspended, switched off or is waking, with their groups.

**Charts:** [tremolo](charts/tremolo/values.yaml) and
[tremolo-kubernetes-collector](charts/tremolo-kubernetes-collector/values.yaml) have documented values.
The storage and collector sections are passed to the upstream
[victoria-metrics-single](https://docs.victoriametrics.com/helm/victoria-metrics-single/),
[victoria-logs-single](https://docs.victoriametrics.com/helm/victoria-logs-single/),
[victoria-traces-single](https://docs.victoriametrics.com/helm/victoria-traces-single/) and
[opentelemetry-collector](https://github.com/open-telemetry/opentelemetry-helm-charts) charts.

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
