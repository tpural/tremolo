# Dashboards

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
