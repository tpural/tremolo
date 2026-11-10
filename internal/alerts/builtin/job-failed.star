# A Job (often a CronJob's run, such as a backup) with failed pods in the last day.
EVERY = "5m"

def check(now):
    out = []
    q = "max by (k8s_namespace_name, k8s_job_name) (max_over_time(k8s_job_failed_pods[1d])) > 0"
    for s in metrics(q):
        ns, job = s.labels["k8s_namespace_name"], s.labels["k8s_job_name"]
        out.append(alert("job %s/%s failed" % (ns, job), key = ns + "/" + job, namespace = ns))
    return out
