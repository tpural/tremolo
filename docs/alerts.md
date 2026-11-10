# Alerts

Alert rules are code: [Starlark](https://github.com/google/starlark-go) files, a dialect of
Python, each with a `check(now)` function. A rule can read metrics, logs and hosts, do any
calculation, branch on the time, and choose who to tell and how loudly.

```python
# checkout-errors.star
EVERY = "1m"   # how often it runs (default 1m)
FOR = "5m"     # how long it must hold before firing (default 0)

def check(now):
    rps = metric('sum(rate(http_requests_total{service="checkout"}[5m]))')
    errors = metric('sum(rate(http_requests_total{service="checkout",code=~"5.."}[5m]))')
    if rps == None or rps < 1:
        return ok()                       # too quiet to judge
    ratio = errors / rps
    business_hours = now.weekday() < 5 and now.hour in range(9, 18)
    if ratio > 0.05 and business_hours:
        return alert("checkout failing: %d%% errors" % (ratio * 100), severity = "page", notify = "oncall")
    if ratio > 0.05 or (ratio > 0.01 and log_count('service:checkout "payment declined"') > 50):
        return alert("checkout degraded", severity = "warn", notify = "slack")
    return ok()
```

One rule can watch many things at once by returning a list, one alert per key:

```python
def check(now):
    return [
        alert("%s queue backing up" % s.labels["queue"], key = s.labels["queue"])
        for s in metrics('max by (queue) (queue_depth) > 1000')
    ]
```

A rule returns `ok()`, one `alert(...)`, or a list of them, each with its own `key=`. An alert keeps firing while the rule keeps returning it, and resolves when it stops.

| In a rule | |
|---|---|
| `metric(q)` | one number from a MetricsQL query, or `None` when nothing matches |
| `metrics(q)` | every series: a list with `.labels` (dict) and `.value` |
| `series(q, since="1h", step="1m")` | values over time: a list with `.labels`, `.values` and `.times` |
| `log_count(q, since="15m")` | how many log lines match a LogsQL query |
| `logs(q, since="15m", limit=100)` | matching log lines, newest first, as dicts |
| `hosts()` | every host by name, with `.state` (ready, notready, asleep, off, booting, maintenance, unreachable), `.state_by`, `.reason`, `.kind`, `.details` |
| `now` | the time in `timezone`: `.hour`, `.minute`, `.weekday()` (Monday is 0), `.date`, `.unix`, `.time` |
| `alert(summary, severity="warn", notify=None, key="", **labels)` | severity is `page`, `warn` or `info`; `notify` names a channel, escalation, team or person, or a list of them, else the severity's default |
| `load("lib/util.star", "fn")` | shared helpers from files under the rules directory |
| `time`, `math`, `print` | Starlark's time and math modules; `print` shows in **Run now** |

Starlark is Python without imports, files or the network, and with no `a < b < c` chains
(`b in range(a, c)` works). A rule runs for at most 30 seconds.

**Built-in rules**, each replaceable by a file of the same name or turned off with `disable`. Each
stays quiet when its data isn't there.

| Rule | Fires when |
|---|---|
| `host-down` | a host is not ready or not reporting, unless asleep, off or in maintenance (page) |
| `wake-stuck` | a host has been waking or booting for 15 minutes (page) |
| `crash-loop` | a pod restarted 3+ times in 30 minutes |
| `pod-stuck` | a pod is Pending, Failed or Unknown for 15 minutes; Pending is left alone while a host wakes |
| `workload-down` | a Deployment or StatefulSet has fewer ready replicas than desired for 15 minutes, unless its pods are on hosts resting on purpose |
| `job-failed` | a Job, e.g. a CronJob's backup run, had failed pods in the last day |
| `disk-full` | a filesystem is over 90%, or full within a day at its current rate |
| `pvc-filling` | a PersistentVolumeClaim is over 90%, or full within a day; claims on one NFS share are one alert |
| `cert-expiring` | a cert-manager certificate expires within 14 days (page within 3), or isn't ready for an hour |
| `flux-failing` | a Flux object has kept failing for 30 minutes with no success since |
| `possum-errors` | possum's power drivers failed 5+ times in 30 minutes |
| `storage-filling` | one of tremolo's stores will run out of disk within a week, or is over 90% of its size cap |
| `backup-stale` | tremolo's own database hasn't been [backed up](configuration.md#backups) for 36 hours |

**Config** (`--alerts-config`, the chart's `alerts.config`); `${VAR}`s come from the environment:

```yaml
timezone: America/Chicago
url: https://tremolo.example.com        # links in notifications, including "acknowledge"
repeat: 4h                              # resend while firing and unacknowledged; 0s sends once
disable: [disk-full]
notify:                                 # where each severity goes when a rule doesn't say
  page: oncall                          # a channel, an escalation, or a list of them
  warn: [phone, slack]
channels:
  phone:
    type: homeassistant                 # Home Assistant's notify service, e.g. the companion app
    url: http://home-assistant:8123
    service: mobile_app_my_phone
    token: ${HA_TOKEN}
  slack:
    type: slack                         # an incoming webhook
    url: ${SLACK_WEBHOOK}
  mail:
    type: email                         # SMTP, with STARTTLS when offered
    host: smtp.example.com
    port: 587
    username: tremolo@example.com
    password: ${SMTP_PASSWORD}
    from: tremolo@example.com
    to: [me@example.com]
  sms:
    type: twilio
    accountSid: ${TWILIO_SID}
    authToken: ${TWILIO_TOKEN}
    from: "+15551234567"
    to: ["+15557654321"]
  topic:
    type: ntfy
    url: https://ntfy.sh/my-topic
  hook:
    type: webhook                       # POSTs the notification as JSON
    url: https://example.com/hook
escalations:
  oncall:                               # each step after the alert has fired unacknowledged this long
    - notify: mail
    - after: 15m
      notify: slack
    - after: 30m
      notify: [sms, phone]
```

## Writing rules in tremolo

**Alerts → New rule** (or **Edit and replay** on any rule) opens the editor. Editors and admins can
write a rule there and see what it would have done before saving it:

- **Replay** runs the draft over the last hour to seven days of real metrics and logs, as if at each
  moment, through `FOR`, firing and resolving just as the engine would. It shows how many times it
  would have fired, when and for how long, with a chart of what was firing. When you're changing a
  saved rule, it also replays the saved version over the same stretch, so you can compare, and
  shows what the running rule really fired, from its history, as a second row of bars.
  Nothing is saved or sent. Long stretches are checked less often than `EVERY`, at most 300 times,
  and it says so. `hosts()` sees each host as it was, from the [recorded host states](hosts.md#state-history);
  runs from before recording began use today's states, and the replay says which.
- **Run once now** runs the draft once and shows what it found and its `print()` output.
- **Save** keeps every version with who saved it and a note. **Versions** lists them; load one into
  the editor and save it to roll back. The newest version starts running straight away.
- **Export .star** downloads the rule, to keep it in git as a file instead.

Rules written here live in tremolo's database. One named like a built-in replaces it (delete yours
to go back). Rules from files in the rules directory are read-only here: change them in the files,
or copy one to make a new rule. If a file and an editor rule have the same name, the file wins.

## People and teams

Anywhere a channel name goes (a rule's `notify`, a severity default, an escalation step) you can
also name a [user or team](users.md). A person is reached through their own **contacts**, set on
their profile: each is a channel from this config plus their address on it, and a delay. A team
reaches each member that way, or, if the team has its own notify targets, goes there instead.

| Channel type | A person's address is |
|---|---|
| `email` | their email address |
| `twilio` | their phone number |
| `homeassistant` | their notify service, e.g. `mobile_app_alex_phone` |
| `ntfy` | their topic on the channel's server |
| `slack` | their member ID, so the message mentions them |
| `webhook` | sent as `to` in the JSON |

So channels are the *how* (one SMTP server, one Twilio account) and people say *where*. A channel
used only for people can leave out its own `to`.

Plan [maintenance](maintenance.md) to hold alerts while you work.

## Triage

- **Acknowledge** stops escalation and repeats, and tells everyone already notified who has it. It
  still sends "resolved" when the alert ends.
- **Take** acknowledges and assigns the alert to you.
- **Forward** hands it to a person or team with a note. It becomes unacknowledged, they're told at
  once ("Forwarded to you … From tim: can you look?"), and it escalates on their contacts from then.
- **Notes** go on the alert's timeline, next to when it fired, who was notified (or couldn't be),
  and every acknowledgement and forward.

Every notification has an acknowledge link. Each person gets their own, so acknowledging from a
phone needs no sign-in and still records who it was. Links open a confirm button rather than
acknowledging on their own, so chat link previews can't.

Providers live in `internal/alerts/notify`, one file each; a new one registers a type name and reads
its own settings.

The **Alerts** page shows what's firing and pending (all, or just yours and your teams'), lets you silence an alert or a whole rule, shows
each rule's source and last run, and runs a rule on demand without changing anything.

**History** lists alerts that resolved, newest first: how long each lasted, how long until someone
acknowledged it and who, and its whole timeline. Search it by summary or rule. It's kept for
`--history-retention` (a year by default).

### Alerts as telemetry

Alerts are also written to the metrics and logs stores, so you can chart them, put them beside the
metrics that caused them, and alert on them like anything else:

- **`tremolo_alert`**, every 30 seconds, one series per pending or firing alert, labelled `alert`,
  `rule`, `severity`, `state`, `acked`, `silenced` and `assignee`. Its value is when it started
  pending, or firing (Unix seconds). For example, unacknowledged pages:
  `count(last_over_time(tremolo_alert{state="firing", severity="page", acked="false"}[1m]))`.
- **`stream:="tremolo-alerts"`** in the logs: one event per thing that happened, with `event`
  (`pending`, `firing`, `notified`, `failed`, `acknowledged`, `forwarded`, `taken`, `note`,
  `resolved`, `silenced` or `unsilenced`), `alert`, `rule`, `key`, `severity`, `by`, `to`, `note`
  and the alert's labels. Every notification is there, including repeats and resolved messages,
  with `about` saying which, so "was I paged, and when?" is a search:
  `stream:="tremolo-alerts" event:=notified to:~"tim"`.

### Runbooks

A runbook is a Markdown note on how to handle a rule's alerts, kept in tremolo's database with every
version. Each covers one or more rules; an alert links to its rule's runbook on the Alerts page, and
notifications carry the link (a **Runbook** button on Home Assistant, a line in email).

Runbooks grow from real fixes. Open a resolved alert in **History**, say how it was fixed, and add it
to the rule's runbook (or start one): tremolo appends a dated section with the alert, when it fired,
how long it lasted, who acknowledged it and the fix. The next person, or an assistant reading the
API, sees how it was solved before.

| | |
|---|---|
| `GET /api/runbooks` (`?rule=`) | runbooks as JSON |
| `GET /api/runbooks/{id}/markdown` | one runbook as plain Markdown |
| `GET /api/alerts` | includes `runbooks`, each rule's runbook ids |
| `POST /api/alerts/history/{entry}/fix` | `{"fix": "...", "runbook": "<id>" or "new"}` |

To start with some, put Markdown files in `--runbooks-dir` (the chart's `runbooks` values). Each is
added once, named by its file, and edited in tremolo after that. A front matter line names its rules:

```markdown
---
rules: [backup-stale]
---
# Restore tremolo's database
```

### Weekly digest

With `digest` in the alerts config, tremolo sends one summary a week to the channels named: alerts
by severity and the noisiest rule, average time to acknowledge, what's firing, backup age and whether
the heartbeat went out all week, disks over 70%, how much each store grew, and fixes noted.

```yaml
digest:
  at: Mon 08:00        # a weekday and time in the alerts timezone
  notify: phone
```

The Alerts page shows it beside the channels, with a preview of what it would say now and **Send now**.

### Heartbeat

tremolo can't tell you it's down, so it can tell something else it's up. With a heartbeat URL, every
minute it checks that alerting works end to end (every rule has run recently and not all of them
failed, and the metrics store, logs store and database answer) and only then sends a `GET` to the
URL. A watcher outside your cluster pages you when the pings stop: tremolo crashed, a node or
storage died, a release broke rules, or the whole site lost power.

With [Healthchecks.io](https://healthchecks.io), make a check with a 1 minute period and a few
minutes' grace, then give tremolo its ping URL. The URL is a credential, so in the chart keep it in
the `alerts.envFromSecret` Secret and name the variable:

```yaml
alerts:
  envFromSecret: tremolo-alerts-env    # holds TREMOLO_HEARTBEAT_URL
heartbeat:
  url: ${TREMOLO_HEARTBEAT_URL}
```

The Alerts page shows when the last ping went out, or why it didn't, and `tremolo_heartbeat_ok` is
1 or 0 after each check. Don't run the watcher in the cluster tremolo watches: it would go down with it.

The **Overview** shows alerts firing by severity and the latest alert activity.
