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
| `--runbooks-dir` | | Runbooks (`.md`) added on start when missing (see [runbooks](alerts.md#runbooks)) |
| `--host-lookback` | `720h` | How long a host that stopped sending metrics stays listed as unreachable; match metrics retention |
| `--data-dir` | | Directory for tremolo's database. Empty keeps it in memory, so it's lost on restart |
| `--backup-dir` | | Where to keep copies of the database (see [backups](#backups)); empty turns them off |
| `--backup-every` / `--backup-keep` | `24h` / `7` | How often to back up, and how many copies to keep |
| `--history-retention` | `8760h` | How long resolved alerts and the audit log are kept |
| `--reset-admin` | | If every admin is locked out: make `admin` an admin again (see [users](users.md)) |
| `--asleep-taints` | | Comma-separated node taints that mean asleep on purpose |
| `--possum-url` | | Turns on the possum provider |
| `--rules-dir` | | Alert rules (`.star`); reloaded when they change |
| `--alerts-config` | | Alert timezone, channels and routing (YAML) |
| `--heartbeat-url` | | URL to ping while alerting works (see [alerts](alerts.md#heartbeat)); `${VAR}`s come from the environment |
| `--heartbeat-every` | `1m` | How often to check alerting and ping |
| `--query-max-range` | `744h` | Longest time range, `[window]` or `offset` a query may use |
| `--query-max-log-rows` | `2000` | Most log rows or spans one query returns |
| `--query-timeout` | `30s` | How long a query may run before it's stopped |
| `--query-concurrency` | `8` | Queries one person or token may run at once; more wait up to 10s, then get 429 |
| `--assistant-max-range` | `168h` | The same for [assistant tokens](users.md#assistant-tokens) |
| `--assistant-max-log-rows` | `500` | The same for assistant tokens; they also run 2 queries at a time |

### Query limits

Every query through tremolo's API is checked before it reaches VictoriaMetrics, VictoriaLogs or
VictoriaTraces, so a dashboard or an assistant can't tie them up. A query over a longer range is
refused with a reason (`the time range is over 31d; narrow it`). A logs query without a `start`
looks back 1h, and one without a `limit` (or with a bigger one) gets `--query-max-log-rows`. A
metrics range query's `step` is raised to keep under 11,000 points. Live tails aren't limited.
Assistant tokens' reads are in the audit log, each query once a minute.

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
same data alongside tremolo.

The **Storage** page (in the menu) shows how much each store holds, how much room is left, and its
retention and size cap, with charts of growth and of what each sender pushes, and the metric names
and log streams that take the most room. tremolo reads the stores' own `/metrics` every minute and
writes `tremolo_storage_data_bytes`, `_free_bytes`, `_total_bytes`, `_max_bytes` and `_rows_total`
(by `store`), and `tremolo_ingest_bytes_total` and `_pushes_total` (by `source` and `signal`). The
built-in `storage-filling` rule warns when a store's disk will fill within a week, or its data
nears its size cap, past which the oldest data goes early.

tremolo's own state is one SQLite file, `tremolo.db` in `--data-dir`. Users, their contacts, teams,
sessions, API tokens and links to external sign-in providers are tables; so are resolved alerts and
the audit log. Alert state, silences and maintenance windows are kept as small JSON documents. It's built in, so there's nothing
else to run. Only one tremolo may use the file at a time. It uses a rollback
journal rather than WAL, so it works on NFS. An older tremolo's JSON files (`users.json`,
`alerts.json`, `maintenance.json`) are imported on the first start and renamed to `*.imported`.

### Backups

With `--backup-dir`, tremolo copies its database there every `--backup-every` (a day), as
`tremolo-YYYYMMDD-HHMMSS.db`, and keeps the newest `--backup-keep` (7). Copies are made while it runs
and are complete databases. Counting starts from the newest copy, so restarts don't make more.
`tremolo_backup_age_seconds` says how old the newest is, and the built-in `backup-stale` rule alerts
when it's over 36 hours.

The chart turns backups on, into `backups/` on the data volume. That covers a bad upgrade or a
mistake, not losing the volume: set `backup.existingClaim` to a PersistentVolumeClaim on other
storage to keep them apart.

To restore, stop tremolo, put the copy in place of `tremolo.db` (and remove any `tremolo.db-journal`),
and start it again. In Kubernetes:

```sh
kubectl -n tremolo scale deploy/tremolo --replicas=0
kubectl -n tremolo run restore --rm -it --restart=Never --image=busybox --overrides='{"spec":{"containers":[{
  "name":"restore","image":"busybox","stdin":true,"tty":true,"command":["sh"],
  "volumeMounts":[{"name":"data","mountPath":"/data"}]}],
  "volumes":[{"name":"data","persistentVolumeClaim":{"claimName":"tremolo-data"}}]}}'
# in the pod: ls /data/backups; cp /data/backups/tremolo-….db /data/tremolo.db; rm -f /data/tremolo.db-journal
kubectl -n tremolo scale deploy/tremolo --replicas=1
```

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
| `GET /api/alerts/history?q=&rule=&before=` | resolved alerts, newest first, with their timelines |
| `POST /api/alerts/ack`, `take`, `forward`, `note` (`{"id", "to", "note"}`) | triage |
| `POST /api/alerts/silences`; `DELETE /api/alerts/silences/{id}` | silence |
| `GET`, `POST /api/ack/{token}` | a notification's acknowledge link; no sign-in needed |
| `POST /api/login`, `/api/logout`; `GET`, `PUT /api/me` | signing in, and your profile and contacts |
| `GET /api/me/sessions`; `DELETE /api/me/sessions/{id}` | where you're signed in; sign one out |
| `GET`, `POST /api/me/tokens`; `DELETE /api/me/tokens/{id}` | your API tokens |
| `DELETE /api/users/{name}/sessions` | sign a user out everywhere, for admins |
| `GET /api/users`, `/api/teams`; `POST`, `PUT`, `DELETE` for admins | users and teams |
| `GET /api/audit?actor=&before=` | the audit log, for admins |
| `POST /api/alerts/rules/{name}/test` | run a rule now without notifying anyone |
| `PUT`, `DELETE /api/alerts/rules/{name}` (`{"source", "note"}`) | save a new version of an editor rule, or delete it |
| `GET /api/alerts/rules/{name}/versions`, `/versions/{n}` | an editor rule's versions |
| `POST /api/alerts/replay` (`{"source", "hours"}`) | what a rule would have done over the last hours; `0` runs it once |
| `GET /api/calendar?from=&to=` | every time maintenance is on in a range (RFC 3339, up to 100 days) |
| `GET /api/maintenance`; `POST`, `PUT`, `DELETE /api/maintenance/{id}` for editors | maintenance periods (`{"title", "start", "end", "every", "repeatUntil", "rules", "hosts", "note"}`) |
| `GET /healthz`, `/api/version` | health and version |
