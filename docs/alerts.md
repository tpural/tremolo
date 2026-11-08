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
| `alert(summary, severity="warn", notify=None, key="", **labels)` | severity is `page`, `warn` or `info`; `notify` is a channel or escalation name, or a list, else the severity's default |
| `load("lib/util.star", "fn")` | shared helpers from files under the rules directory |
| `time`, `math`, `print` | Starlark's time and math modules; `print` shows in **Run now** |

Starlark is Python without imports, files or the network, and with no `a < b < c` chains
(`b in range(a, c)` works). A rule runs for at most 30 seconds.

**Built-in rules**, each replaceable by a file of the same name or turned off with `disable`:
`host-down` (a host not ready or not reporting, unless asleep, off or in maintenance), `crash-loop`
(a pod restarting 3+ times in 30 minutes) and `disk-full` (over 90%, or full within a day at its
current rate).

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

**Acknowledging** an alert, from the Alerts page or the link in its notification, stops its
escalation and repeats and tells the channels already notified; it still sends "resolved" when it
ends. Links open a confirm button rather than acknowledging on their own, so chat link previews
can't. Providers live in `internal/alerts/notify`, one file each; a new one registers a type name
and reads its own settings.

The **Alerts** page shows what's firing and pending, lets you silence an alert or a whole rule, shows
each rule's source and last run, and runs a rule on demand without changing anything. Firing and
resolved events are written to the logs (`stream:="tremolo-alerts"`), so the history is searchable.
