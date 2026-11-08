# A host is down and nothing says that's on purpose: hosts asleep, switched
# off, waking or in maintenance are left alone.
FOR = "5m"

def check(now):
    out = []
    for h in hosts().values():
        if h.state == "notready":
            out.append(alert("%s is not ready" % h.name, key = h.name, severity = "page", host = h.name))
        elif h.state == "unreachable":
            out.append(alert("%s stopped sending metrics" % h.name, key = h.name, severity = "page", host = h.name))
    return out
