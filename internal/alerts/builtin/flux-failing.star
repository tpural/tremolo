# A Flux object that keeps failing: warnings for 30 minutes with no success
# since. One-off failures (a git fetch, a chart not published yet) are left alone.
EVERY = "5m"

def check(now):
    out = []
    events = logs('{k8s.namespace.name="flux-system"} object.kind:=Event object.regarding.name:*', since = "1h", limit = 1000)
    latest, failing = {}, {}
    for e in events:  # newest first
        obj = e.get("object.regarding.kind", "") + "/" + e.get("object.regarding.name", "")
        warn = e.get("object.type", "") == "Warning"
        if obj not in latest:
            latest[obj] = e
        if warn:
            failing.setdefault(obj, []).append(e)
    for obj, warns in failing.items():
        last = latest[obj]
        if last.get("object.type", "") != "Warning" or len(warns) < 3:
            continue
        if since_minutes(now, warns[-1]) < 30:
            continue
        out.append(alert(
            "flux %s keeps failing: %s" % (obj, last.get("object.note", last.get("object.reason", ""))[:200]),
            key = obj,
            reason = last.get("object.reason", ""),
        ))
    return out

def since_minutes(now, e):
    return (now.unix - time.parse_time(e["_time"]).unix) / 60
