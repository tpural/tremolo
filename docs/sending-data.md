# Sending data

tremolo accepts the formats your tools already speak, so there is usually nothing new to install on
the sending side.

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
[`deploy/host/otelcol.yaml`](../deploy/host/otelcol.yaml) and `TREMOLO_URL` / `TREMOLO_TOKEN` set.

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

## Tokens

Without tokens, anyone who can reach ports 4317/4318 can write. Before exposing them, give each sender
a token:

```yaml
# tokens.yaml, passed with --ingest-tokens-file (chart: ingest.tokens or ingest.existingSecret)
ci: 6f1c…
laptop: 93ab…
```

Senders send `Authorization: Bearer <token>`. The token's name is added to everything it sends as a
`source` label (metrics) or field (logs), so you can tell senders apart.

The Kubernetes collector takes its token from `kubernetes.tremolo.token` (with Flux, set it from a
Secret through the HelmRelease's `valuesFrom`). Its pods read the token when they start, so restart
them after changing it: `kubectl rollout restart ds/tremolo-agent-agent deploy/tremolo-cluster`.

## Browsers (Grafana Faro)

tremolo takes [Grafana Faro](https://github.com/grafana/faro-web-sdk) batches at `POST /faro/collect`
on the ingest port: JavaScript errors with their stacks, logs, page views and other events, and Web
Vitals. They become:

- the `browser` logs stream, one line per error, log or event, with `kind` (`exception`, `log`,
  `page_view`, `event`), `app`, `release`, `page`, `browser`, `os`, `mobile` and `session` (Faro's
  random id), and `stack` for errors;
- `browser_web_vitals{app, page, name}` (`lcp`, `inp`, `fcp` and `ttfb` in milliseconds, `cls`
  unitless), one sample per measurement, so `quantile_over_time(0.75, …)` gives p75;
- `browser_events{app, kind, page}`, 1 per line above, for counts with `sum_over_time`.

Pages are kept as paths, with ids such as `/posts/42` folded to `/posts/:id` and no query string, so
searches and tokens in URLs aren't stored.

Send it through your site's own server rather than straight from browsers: the ingest token stays
on the server, tremolo stays off the internet, and there's no cross-origin request. With nginx:

```nginx
location = /rum {
    limit_req zone=rum burst=20 nodelay;     # limit_req_zone $binary_remote_addr zone=rum:1m rate=2r/s;
    client_max_body_size 256k;
    proxy_set_header Authorization "Bearer <token>";
    proxy_pass http://tremolo.tremolo.svc:4318/faro/collect;
}
```

and start Faro with `url: "/rum"`.

### The Frontend tab

Each app that sends data shows under **Frontend** on its own: page views and errors today, and p75
LCP, INP and CLS over 7 days rated against Google's thresholds. Its page is a dashboard tremolo
makes: visits, errors, Web Vitals by page, and the latest errors. To leave pages out of an app's
numbers, such as an admin page only you use, or to add its server's memory, CPU, restarts and errors,
set them in the chart:

```yaml
frontend:
  apps:
    shop:
      namespace: shop               # where its server runs
      ignorePages: [/admin, /login]
```

An [assistant token](users.md#assistant-tokens) sees the apps its metrics filters match: use the
**Browser app** filter, since browser data is labelled by `app`, not namespace.
