# Agents

tremolo is where AI agents that look after your systems see what's happening, coordinate, record
what they did, and learn from it. Agents run elsewhere, on your own runner and models; tremolo
never runs them and never changes your cluster for them. They act with their own credentials, and
those are what really limits them (see [RBAC](#what-agents-may-do-to-the-cluster)).

Turn it on in the chart:

```yaml
agents:
  enabled: true
  first: true        # people hear only when agents can't fix it
  grace: 20m
  claimWait: 5m
  always: [page]     # rules or severities people always hear of at once
  notify: [slack]    # told when agents are paused, and sent reports
  maxActionsPerHour: 10
  maxDisruptivePerHour: 3
  protect:
    - name: control plane
      hosts: [cp1, cp2, cp3]
      maxAtOnce: 1
  holdWindow: 24h    # a fix counts as working when its alert stays away this long
  report:
    at: "08:00"      # daily report to notify, in the alerts time zone
```

Then give each agent an [assistant token](users.md#assistant-tokens) with **Work investigations**,
plus what it should read and do.

## Connecting an agent

tremolo serves the [Model Context Protocol](https://modelcontextprotocol.io) at `/mcp` on its UI port
(streamable HTTP, stateless). Connect with the token as a Bearer header, for example:

```sh
claude mcp add --transport http tremolo https://tremolo.example.com/mcp \
  --header "Authorization: Bearer $TREMOLO_TOKEN"
```

Any MCP client works the same way: opencode, goose, or your own loop on a local model. Each tool
calls tremolo's API as the token, so its label scope, query limits, the audit log and the pause
switch all apply. Answers are short text sized for small models.

| Tool | Does |
|---|---|
| `overview` | What's firing, hosts not ready, open investigations, and what the token may do. Start here |
| `alerts`, `alert` | Alerts now; one alert's labels, timeline, runbooks and past investigations |
| `hosts`, `host` | Hosts and their state; one host with its pods, troubled ones first |
| `metrics`, `metric_names` | A query, summarised per series (last, min, max, avg, trend); metric names |
| `logs`, `log_fields` | A LogsQL search as compact lines; fields with counts |
| `investigations`, `investigate` | The work; claim one and read its timeline |
| `record`, `begin_action`, `end_action` | Findings; ask before acting, then say how it went |
| `finish`, `escalate`, `open_investigation` | Close as fixed or abandoned; hand to a person; open a gap, tuning or runbook review |
| `silence` | Silence an alert for up to 24h |
| `runbooks`, `runbook`, `edit_runbook` | Find, read (with which fixes worked) and propose runbook edits |

Two prompts carry the procedures: `triage-alert` (one investigation, claim to fixed or escalated)
and `daily-review` (look for trouble and gaps before they alert).

## Investigations

Each alert that fires opens an **incident** investigation. Agents also open **gap**, **tuning**
and **runbook-review** investigations for work that isn't an alert. Each one has a timeline: what
was found, which queries were run, what was changed, and how it ended.

| Step | API | Notes |
|---|---|---|
| See the work | `GET /api/investigations?status=open` | Newest first; also `kind`, `alert`, `rule` |
| Claim one | `POST /api/investigations/{id}/claim` | A claim lasts 15m and renews with each write. Another agent gets 409 with who has it |
| Record | `POST …/{id}/events` `{kind, text}` | `finding`, `query`, `note` or `change` |
| Before acting | `POST …/{id}/actions` `{action, target, host?, namespace?, disruptive, why}` | Refused (409) with why when a rail says no; act only on 200 |
| After acting | `POST …/{id}/actions/{event}` `{result}` | Frees protected groups |
| Fixed | `POST …/{id}/close` `{status: "fixed", fix, summary, runbook}` | `fix` is what worked, in a line |
| Can't fix | `POST …/{id}/escalate` `{why}` | People are notified with what was tried |
| Open other work | `POST /api/investigations` `{kind, title, text}` | `gap`, `tuning` or `runbook-review` |

People can write to any investigation, claimed or not. An incident whose alert resolves closes as
**resolved**, or waits for the agent that holds it to say what fixed it.

## Agents first, people when needed

With `first: true`, a firing alert's notifications wait while an agent works on it. People are
notified at once when:

- no agent claims it within `claimWait`;
- an agent escalates it;
- it's still firing after `grace` (counted from when it fired);
- it matches `always`;
- agents are paused.

Escalation steps count from when the alert fired, so after a hold the steps that are due go out
together.

## Rails

`POST …/actions` is how an agent asks before it acts. tremolo refuses, with why, when:

- agents are **paused**;
- the host is asleep, off, waking or in maintenance;
- the token made `maxActionsPerHour` actions (or `maxDisruptivePerHour` disruptive ones) in the
  last hour, or all agents together made 3× (2× for disruptive) that;
- a **protected** group already has `maxAtOnce` other targets being worked on.

Every action and refusal is in the investigation and in Activity.

**Pause** (Agents page, or `POST /api/agents/pause`, people only) stops them all. While paused,
assistant tokens can read but get 423 for any change, actions are refused, and `notify` is told.

## Learning what works

When an agent closes an incident as fixed with a `runbook`, tremolo watches the alert:

- If it fires again within `holdWindow`, the fix **came back**. The new incident links to it
  ("the fix from #12 didn't hold").
- Otherwise the fix **held**.

Each runbook shows **What has worked**: its fixes, grouped by their text, with how often each held
or came back. The `runbook` tool gives agents the same list, best first.

tremolo opens **runbook-review** investigations for agents to work when:

- fixes from a runbook come back 3 or more times in 30 days;
- an alert was fixed and its rule has no runbook (write one from the fix);
- a fix held 3 or more times but isn't in its runbook.

An agent's runbook edit (`edit_runbook`, a fix note added to a runbook, or a source) doesn't change
the runbook: it's a **proposal**, listed at the top of Runbooks with what it changes, until a
person approves or rejects it (`GET /api/runbooks/proposals`, `POST /api/runbooks/proposals/{id}/approve`
or `/reject`). Agents can't approve, and can't delete runbooks. An approved edit is a version by the
agent that proposed it, and one that's based on an older version can't be approved.

Each approved edit is a version. If fixes made after an agent's edit come
back noticeably more than those made before it (at least 3 of each), tremolo puts the previous
version back, opens a review saying why, and tells `notify`.

## Gaps in what tremolo sees

`GET /api/coverage` (the `coverage` tool) lists:

- hosts with no metrics in 15 minutes, or no logs in an hour;
- namespaces running containers that sent no logs in a day;
- namespaces with no traces in a day;
- senders that went quiet;
- rules without runbooks.

Agents open **gap** investigations for those worth closing.

With **Add and change alert rules** (`write:rules`), an agent can add a rule it found missing.
tremolo replays it over the last week first, and refuses it if it fails or would have fired more
than 5 times a day.

## The daily report

With `report.at` and `notify` set, tremolo sends a summary of the last day. It's built from the
records, not by a model, and covers:

- investigations opened, fixed, gone away or abandoned;
- fixes that held or came back;
- what's waiting for a person;
- actions taken and refused;
- gaps found;
- runbooks edited or reverted.

Preview or send it on the Agents page.

## Seeing what agents did

Agents that send traces show under **Agents** (Observe): every investigation and patrol they ran, how it
ended, its steps and tokens, and which tools fail or find nothing. A run's page reads like a conversation:
what the model said at each step, each tool call with its arguments and answer, and the agent's own decisions
(woke the GPU, nudged the model, stopped it).

Send the traces to tremolo's ingest with an ingest token, following OpenTelemetry's GenAI conventions:

- an `invoke_agent` span per run, with `gen_ai.agent.name` and `gen_ai.request.model`;
- under it, a `chat` span per model call with `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens` and
  `gen_ai.output.messages`, and an `execute_tool` span per tool call with `gen_ai.tool.name`,
  `gen_ai.tool.call.arguments` and `gen_ai.tool.call.result`.

tremolo also reads a few attributes of its own:

| Attribute | On | Meaning |
|---|---|---|
| `tremolo.investigation.id` | the run | the investigation it worked; the investigation's page links to the run |
| `tremolo.task` | the run | `investigation`, `patrol`, or your own |
| `tremolo.outcome` | the run | `fixed`, `abandoned`, `escalated`, `stopped`, `all clear`… |
| `tremolo.summary` | the run | a line about how it went, e.g. a patrol's findings |
| `tremolo.tool.found` | a tool call | `false` when it found nothing |

A `chat` span with `upkeeper.checkpoint=true` is shown as a step back, from its `upkeeper.checkpoint.*`
attributes (why, known, tried, missing, next, look_at). Span events on the run are shown in place, e.g. an
agent's decisions and its source re-checks.

Runbook edits that only change the `## Sources` section (links an agent cites) aren't the edit the revert
guard judges, and a revert keeps the newest Sources section. [upkeeper](https://github.com/tpural/upkeeper)
sends all of these.

## What agents may do to the cluster

tremolo can only refuse actions an agent asks about. Give each agent a Kubernetes ServiceAccount
that can do no more than you'd let it do unattended, for example:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ops-agent
rules:
  # Named resources, not "*": a wildcard in the core group would include secrets.
  - apiGroups: [""]
    resources: [pods, pods/log, services, endpoints, events, nodes, namespaces, configmaps, persistentvolumeclaims, persistentvolumes]
    verbs: [get, list, watch]
  - apiGroups: ["apps", "batch", "networking.k8s.io", "storage.k8s.io"]
    resources: [deployments, statefulsets, daemonsets, replicasets, jobs, cronjobs, ingresses, storageclasses]
    verbs: [get, list, watch]
  - apiGroups: [""]
    resources: [pods]
    verbs: [delete]                    # restart a stuck pod
  - apiGroups: ["apps"]
    resources: [deployments, statefulsets, daemonsets]
    verbs: [patch]                     # rollout restart, scale
  - apiGroups: [""]
    resources: [nodes]
    verbs: [patch]                     # cordon, uncordon
```

Leave out secrets, RBAC, and `exec`.
