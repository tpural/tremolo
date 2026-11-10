# A Deployment or StatefulSet with fewer ready replicas than it wants. Ones whose
# pods are on hosts asleep, off or in maintenance on purpose are left alone.
FOR = "15m"

KINDS = {
    "deployment": ("k8s_deployment_available", "k8s_deployment_desired"),
    "statefulset": ("k8s_statefulset_ready_pods", "k8s_statefulset_desired_pods"),
}

def check(now):
    resting = [h.name for h in hosts().values() if h.state in ("asleep", "off", "booting", "maintenance")]
    out = []
    for kind, (ready, desired) in KINDS.items():
        by = "k8s_namespace_name, k8s_%s_name" % kind
        q = "max by (%s) (%s) - max by (%s) (%s) > 0" % (by, desired, by, ready)
        for s in metrics(q):
            ns, name = s.labels["k8s_namespace_name"], s.labels["k8s_%s_name" % kind]
            if on_resting(ns, name, resting):
                continue
            out.append(alert(
                "%s %s/%s is missing %d ready replicas" % (kind, ns, name, s.value),
                key = "%s/%s/%s" % (kind, ns, name),
                namespace = ns,
            ))
    return out

def on_resting(ns, name, resting):
    if not resting:
        return False
    pods = 'k8s_pod_phase{k8s_namespace_name="%s", k8s_pod_name=~"%s-.*"}' % (ns, name)
    for p in metrics("max by (k8s_node_name) (%s)" % pods):
        if p.labels.get("k8s_node_name", "") in resting or p.labels.get("k8s_node_name", "") == "":
            return True
    return False
