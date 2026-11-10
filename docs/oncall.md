# On-call and maintenance

**On-call** in the sidebar is a calendar of who's paged when, and of maintenance that holds alerts.
Times show in your browser's zone; handoffs and repeating maintenance follow the alerts `timezone`.

## Schedules

A schedule is a rotation: its members take turns in order, each for the turn's length (daily, weekly,
every two weeks or every N days), handing off at the first handoff's time of day. A Monday 09:00
handoff stays at 09:00 across daylight saving.

Name a schedule anywhere a person or team goes: a rule's `notify`, a severity default, an escalation
step, a team's own notify targets, or **Forward**. It reaches whoever is on call at that moment,
through their contacts. If a turn ends while an alert is unacknowledged, the next person is paged
too. Schedules share names with users and teams.

```yaml
notify:
  page: primary
escalations:
  primary:
    - notify: platform-oncall
    - after: 15m
      notify: platform-backup     # same people, starting a turn later
    - after: 30m
      notify: platform            # the whole team
```

**Cover a shift** on any day puts someone on call instead of the rotation for a while: anyone can
cover for themselves, and editors can put anyone on. The newest cover wins where two overlap. Editors
make and change schedules. A deleted user leaves their schedules and covers.

`tremolo_oncall{schedule, person}` in the metrics store is who was on call, every 30 seconds.

## Maintenance

A maintenance period holds alerts: once, or every day or week (until a date, if you like). It holds
every alert, or only some **rules**, or only alerts on some **hosts** (by the alert's `host` or
`host_name` label), or both: only those rules on those hosts.

While it's on, the alerts it holds don't start firing and don't notify anyone; the Alerts page shows
them as held. One already firing stays firing but quiet, and resolving says nothing. When it ends,
anything still wrong fires after the rule's `FOR`, counted from the end, so nothing broken is lost.
The alert's timeline and the `tremolo-alerts` log stream note when it was held and released.

A period for whole hosts (hosts, no rules) also shows those hosts as **in maintenance**. **Start
maintenance** on a host's page is the same thing: a period for that host from now. Ending it there
ends the period; one that repeats is changed on the calendar instead.
