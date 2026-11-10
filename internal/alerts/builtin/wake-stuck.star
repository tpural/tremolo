# A host that started waking or booting and never became ready. host-down skips
# booting hosts, so without this one a failed wake is silent.
EVERY = "1m"

def check(now):
    out = []
    for s in metrics('last_over_time(tremolo_host_state{state="booting"}[2m])'):
        took = now.unix - int(s.value)
        if took > 15 * 60:
            name = s.labels["host_name"]
            out.append(alert(
                "%s has been waking for %d minutes" % (name, took / 60),
                key = name,
                severity = "page",
                host = name,
            ))
    return out
