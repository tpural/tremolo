# A pod whose containers keep restarting.
FOR = "5m"

def check(now):
    out = []
    # increase_prometheus, unlike increase, doesn't count a series' first value: when labels
    # change (a collector upgrade, a rename), every pod's lifetime restarts would look new.
    restarts = "sum by (k8s_namespace_name, k8s_pod_name) (increase_prometheus(k8s_container_restarts[30m])) >= 3"
    for s in metrics(restarts):
        ns, pod = s.labels["k8s_namespace_name"], s.labels["k8s_pod_name"]
        out.append(alert(
            "%s/%s restarted %d times in 30 minutes" % (ns, pod, s.value),
            key = ns + "/" + pod,
            namespace = ns,
            pod = pod,
        ))
    return out
