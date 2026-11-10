# tremolo's own database hasn't been backed up for a day and a half. Quiet when
# backups are off: then there's no tremolo_backup_age_seconds.
FOR = "10m"

def check(now):
    out = []
    for s in metrics("max(last_over_time(tremolo_backup_age_seconds[15m])) > 36 * 3600"):
        out.append(alert(
            "tremolo's database hasn't been backed up for %d hours" % int(s.value / 3600),
            key = "tremolo",
        ))
    return out
