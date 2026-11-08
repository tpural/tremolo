# Configuration

## Server flags

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

tremolo runs anywhere: as a container, a binary, or in Kubernetes. Kubernetes access is optional and
picked up automatically (in-cluster or `KUBECONFIG`); with it, nodes also show their Kubernetes state and pods.

## Helm charts

 [tremolo](../charts/tremolo/values.yaml) and
[tremolo-kubernetes-collector](../charts/tremolo-kubernetes-collector/values.yaml) have documented values.
The storage and collector sections are passed to the upstream
[victoria-metrics-single](https://docs.victoriametrics.com/helm/victoria-metrics-single/),
[victoria-logs-single](https://docs.victoriametrics.com/helm/victoria-logs-single/),
[victoria-traces-single](https://docs.victoriametrics.com/helm/victoria-traces-single/) and
[opentelemetry-collector](https://github.com/open-telemetry/opentelemetry-helm-charts) charts.

Traces are off by default in the chart while VictoriaTraces is before 1.0; turn them on with
`--set traces.enabled=true`. For a permanent UI address, enable the Gateway API route (`httpRoute`).
Alert rules go in `alerts.rules` or an existing ConfigMap, the alert config in `alerts.config`, and
secrets for it in a Secret named by `alerts.envFromSecret`.

## Storage

tremolo keeps no telemetry itself: metrics, logs and traces live in VictoriaMetrics, VictoriaLogs and
VictoriaTraces, which you size and retain as you like (the chart and Compose file set 30, 14 and 7 days).
Because the storage speaks PromQL, LogsQL and the Jaeger API, Grafana or any other tool can read the
same data alongside tremolo. tremolo's own state (maintenance windows, alert state, silences) is a few
JSON files in `--data-dir`.

## Security

The ingest ports accept writes from anyone who can reach them unless you set
[tokens](sending-data.md#tokens). The UI and its API have no login of their own: keep them on a private
network or put them behind your reverse proxy's authentication (oauth2-proxy, Authelia, a Gateway
policy, …).

## HTTP API

The UI uses the same API you can script against:

| | |
|---|---|
| `GET /api/metrics/query`, `query_range`, `labels`, `series`, `label/{name}/values` | VictoriaMetrics, read-only |
| `GET /api/logs/query`, `tail`, `hits`, `field_names`, `field_values` | VictoriaLogs, read-only |
| `GET /api/traces/…` | the Jaeger query API, when traces are on |
| `GET /api/hosts`, `/api/hosts/{name}/pods` | hosts with their state; pods on a Kubernetes node |
| `PUT` / `DELETE /api/hosts/{name}/maintenance` | start or end a maintenance window |
| `GET /api/dashboards`, `/api/dashboards/{id}` | dashboards |
| `GET /api/alerts` | firing and pending alerts, rules, silences and channels |
| `POST /api/alerts/ack`, `/api/alerts/silences`; `DELETE /api/alerts/silences/{id}` | acknowledge and silence |
| `POST /api/alerts/rules/{name}/test` | run a rule now without notifying anyone |
| `GET /healthz`, `/api/version` | health and version |
