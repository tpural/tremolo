# A filesystem that is nearly full, or will be within a day at the rate it
# filled over the last 6 hours.
EVERY = "5m"
FOR = "15m"

DISKS = 'type!~"tmpfs|overlay|squashfs|nsfs|ramfs"'

def usage(state = ""):
    match = DISKS + (', state="%s"' % state if state else "")
    return "sum by (host_name, mountpoint) (system_filesystem_usage_bytes{%s})" % match

def check(now):
    used, total, free = usage("used"), usage(), usage("free")
    found = {}
    for s in metrics("(%s) / (%s) > 0.9" % (used, total)):
        found[key(s)] = alert(
            "%s %s is %d%% full" % (s.labels["host_name"], s.labels["mountpoint"], s.value * 100),
            key = key(s),
            host = s.labels["host_name"],
        )
    for s in metrics("predict_linear((%s)[6h:5m], 86400) < 0 and (%s) / (%s) > 0.5" % (free, used, total)):
        if key(s) not in found:
            found[key(s)] = alert(
                "%s %s will be full within a day" % (s.labels["host_name"], s.labels["mountpoint"]),
                key = key(s),
                host = s.labels["host_name"],
            )
    return list(found.values())

def key(s):
    return s.labels["host_name"] + ":" + s.labels["mountpoint"]
