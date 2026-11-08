# Hosts

Any machine that sends host metrics (the OpenTelemetry `hostmetrics` receiver, with `host.name` set) is a
host: a server, a VM, a laptop, a CI runner or a Kubernetes node. tremolo lists it with its CPU, memory,
disks, network and logs, and keeps it listed as **unreachable** when it stops reporting
(`--host-lookback`), so a machine that dies doesn't just disappear.

## Servers and Kubernetes nodes

tremolo shows each host as a **Kubernetes node** or a **server**, from how its metrics arrive:
tremolo-kubernetes-collector marks everything it sends with `tremolo.collector=kubernetes` and the
cluster's name (`clusterName` in its values), and the host collector config marks its machines with
`tremolo.collector=host`. Nodes are grouped by cluster, so several clusters can report to one tremolo.
Any other sender of host metrics shows up as a server.

## Host state providers

A host's state comes from its metrics (ready, or unreachable when they stop) and, for Kubernetes
nodes, from the API server (ready, not ready). Providers add the cases that are on purpose, so they don't read as broken:

- **maintenance** (always on): from a host's page, or `PUT /api/hosts/{name}/maintenance` with
  `{"until": "<RFC 3339 time>", "note": "..."}` (omit `until` to keep it until cleared) and
  `DELETE` to end it. It wins over every other state.
- **kubernetes** (when tremolo can reach a Kubernetes API): cordoned nodes are in maintenance; nodes with one of `--asleep-taints` are asleep.
- **possum** (`--possum-url`): nodes [possum](https://github.com/tpural/possum), a power autoscaler,
  has suspended, switched off or is waking, with their groups.

A provider is a small Go type with `Name()` and `Apply(ctx, hosts)` in `internal/hosts`; one that knows
about your power management, inventory or cloud provider fits the same shape.
