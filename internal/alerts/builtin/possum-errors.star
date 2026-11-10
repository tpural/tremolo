# possum's power drivers failing: it can't put hosts to sleep or wake them.
EVERY = "5m"

def check(now):
    out = []
    for s in metrics("sum by (driver) (increase_prometheus(possum_driver_errors_total[30m])) >= 5"):
        d = s.labels["driver"]
        out.append(alert("possum's %s driver failed %d times in 30 minutes" % (d, s.value), key = d, driver = d))
    return out
