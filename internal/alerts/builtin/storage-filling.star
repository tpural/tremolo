# One of tremolo's own stores running out of room: its disk full within a week at
# the rate of the last day, or its data near the size cap (logs and traces drop
# their oldest data early past it, before their retention is up).
EVERY = "15m"
FOR = "30m"

def check(now):
    out = []
    for s in metrics("predict_linear(min by (store) (tremolo_storage_free_bytes)[1d:15m], 7 * 86400) < 0"):
        out.append(alert("tremolo's %s store will run out of disk within a week" % s.labels["store"], key = s.labels["store"]))
    near = "max by (store) (tremolo_storage_data_bytes) / max by (store) (tremolo_storage_max_bytes) > 0.9"
    for s in metrics(near):
        out.append(alert(
            "tremolo's %s store is at %d%% of its size cap; its oldest data goes early" % (s.labels["store"], s.value * 100),
            key = s.labels["store"] + ":cap",
        ))
    return out
