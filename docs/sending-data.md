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
