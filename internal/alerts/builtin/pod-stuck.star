# A pod stuck Pending, Failed or Unknown. Pending pods are left alone while a
# host is waking, since possum wakes hosts for them.
FOR = "15m"

PHASES = {1: "pending", 4: "failed", 5: "in an unknown state"}

def check(now):
    waking = [h for h in hosts().values() if h.state == "booting"]
    out = []
    q = "max by (k8s_namespace_name, k8s_pod_name) (k8s_pod_phase) != 2 and max by (k8s_namespace_name, k8s_pod_name) (k8s_pod_phase) != 3"
    for s in metrics(q):
        phase = int(s.value)
        if phase == 1 and waking:
            continue
        ns, pod = s.labels["k8s_namespace_name"], s.labels["k8s_pod_name"]
        out.append(alert(
            "pod %s/%s is %s" % (ns, pod, PHASES.get(phase, "not running")),
            key = ns + "/" + pod,
            namespace = ns,
            pod = pod,
        ))
    return out
