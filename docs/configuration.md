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
| `--data-dir` | | Directory for tremolo's own state: users, alerts, silences and maintenance windows. Empty keeps it in memory, so it's lost on restart |
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
same data alongside tremolo. tremolo's own state (users, maintenance windows, alert state, silences) is a few
JSON files in `--data-dir`.

## Security

The UI and its API need [signing in](users.md); only the app's static files, `/healthz`,
`/api/version` and acknowledge links work without it. Serve tremolo over HTTPS so session cookies are
marked secure (it follows `X-Forwarded-Proto` from a proxy).

The ingest ports accept writes from anyone who can reach them unless you set
[tokens](sending-data.md#tokens).

## HTTP API

The UI uses the same API you can script against, with an [API token](users.md#api-tokens):

| | |
|---|---|
| `GET /api/metrics/query`, `query_range`, `labels`, `series`, `label/{name}/values` | VictoriaMetrics, read-only |
| `GET /api/logs/query`, `tail`, `hits`, `field_names`, `field_values` | VictoriaLogs, read-only |
| `GET /api/traces/…` | the Jaeger query API, when traces are on |
| `GET /api/hosts`, `/api/hosts/{name}/pods` | hosts with their state; pods on a Kubernetes node |
| `PUT` / `DELETE /api/hosts/{name}/maintenance` | start or end a maintenance window |
| `GET /api/dashboards`, `/api/dashboards/{id}` | dashboards |
| `GET /api/alerts` | firing and pending alerts, rules, silences and channels |
| `POST /api/alerts/ack`, `take`, `forward`, `note` (`{"id", "to", "note"}`) | triage |
| `POST /api/alerts/silences`; `DELETE /api/alerts/silences/{id}` | silence |
| `GET`, `POST /api/ack/{token}` | a notification's acknowledge link; no sign-in needed |
| `POST /api/login`, `/api/logout`; `GET`, `PUT /api/me` | signing in, and your profile and contacts |
| `GET`, `POST /api/me/tokens`; `DELETE /api/me/tokens/{id}` | your API tokens |
| `GET /api/users`, `/api/teams`; `POST`, `PUT`, `DELETE` for admins | users and teams |
| `POST /api/alerts/rules/{name}/test` | run a rule now without notifying anyone |
| `GET /healthz`, `/api/version` | health and version |
