# A pod whose containers keep restarting.
FOR = "5m"

def check(now):
    out = []
    # Each restart is a new series (container_id changes) holding one value, so increase sees
    # nothing. Per container, the highest count minus the lowest in the window is the restarts
    # in it; a label change (a collector upgrade, a rename) doesn't make lifetime restarts look new.
    by = "k8s_namespace_name, k8s_pod_name, k8s_container_name"
    restarts = ("sum by (k8s_namespace_name, k8s_pod_name) (max by (%s) (max_over_time(k8s_container_restarts[30m]))" +
                " - min by (%s) (min_over_time(k8s_container_restarts[30m]))) >= 3") % (by, by)
    for s in metrics(restarts):
        ns, pod = s.labels["k8s_namespace_name"], s.labels["k8s_pod_name"]
        out.append(alert(
            "%s/%s restarted %d times in 30 minutes" % (ns, pod, s.value),
            key = ns + "/" + pod,
            namespace = ns,
            pod = pod,
        ))
    return out
