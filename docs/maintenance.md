# Maintenance

**Maintenance** in the sidebar is a calendar of planned maintenance: times when alerts are held,
once or every day or week (until a date, if you like). Times show in your browser's zone; repeats
keep their time of day in the alerts `timezone`, across daylight saving.

A period holds every alert, or only some **rules**, or only alerts on some **hosts** (by the alert's
`host` or `host_name` label), or both: only those rules on those hosts.

While it's on, the alerts it holds don't start firing and don't notify anyone; the Alerts page shows
them as held. One already firing stays firing but quiet, and resolving says nothing. When it ends,
anything still wrong fires after the rule's `FOR`, counted from the end, so nothing broken is lost.
The alert's timeline and the `tremolo-alerts` log stream note when it was held and released, and
`tremolo_alert` has a `maintenance` label.

A period for whole hosts (hosts, no rules) also shows those hosts as **in maintenance**. **Start
maintenance** on a host's page is the same thing: a period for that host from now. Ending it there
ends the period; one that repeats is changed on the calendar instead.

Editors plan and change maintenance; it's in the audit log.
