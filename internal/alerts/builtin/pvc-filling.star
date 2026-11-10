# A PersistentVolumeClaim that is nearly full, or will be within a day at the
# rate it filled over the last 6 hours. Claims on one NFS share all report the
# share, so claims with the same size are one alert.
EVERY = "5m"
FOR = "15m"

BY = "namespace, persistentvolumeclaim"

def check(now):
    used = "max by (%s) (kubelet_volume_stats_used_bytes)" % BY
    total = "max by (%s) (kubelet_volume_stats_capacity_bytes)" % BY
    free = "max by (%s) (kubelet_volume_stats_available_bytes)" % BY
    sizes = {name(s): s.value for s in metrics(total)}
    full = group(metrics("(%s) / (%s) > 0.9" % (used, total)), sizes)
    filling = group(metrics("predict_linear((%s)[6h:5m], 86400) < 0 and (%s) / (%s) > 0.5" % (free, used, total)), sizes)
    out = []
    for size, claims in full.items():
        out.append(alert("%s %s %d%% full" % (describe(claims), "are" if len(claims) > 1 else "is", claims[0][1] * 100), key = str(size)))
    for size, claims in filling.items():
        if size not in full:
            out.append(alert("%s will be full within a day" % describe(claims), key = str(size)))
    return out

def name(s):
    return s.labels["namespace"] + "/" + s.labels["persistentvolumeclaim"]

def group(series, sizes):
    by = {}
    for s in series:
        by.setdefault(sizes.get(name(s), name(s)), []).append((name(s), s.value))
    return {k: sorted(v) for k, v in by.items()}

def describe(claims):
    if len(claims) == 1:
        return "volume " + claims[0][0]
    return "volume %s and %d more on the same storage" % (claims[0][0], len(claims) - 1)
