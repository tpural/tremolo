# Users and teams

tremolo has its own accounts. Everyone signs in to see anything; what they can change depends on
their role.

| Role | Can |
|---|---|
| viewer | see everything |
| editor | also acknowledge, take, forward and annotate alerts, silence, set maintenance and run rules |
| admin | also manage users and teams |

## The first admin

On its first start tremolo creates a user named `admin`. Its password is `$TREMOLO_ADMIN_PASSWORD`
if set (the chart's `auth.adminPasswordSecret`), or a random one printed once in tremolo's log:

```
level=WARN msg="created the first user; sign in and change its password" user=admin password=…
```

Sign in, change it on your profile, and add the others under **Users and teams**. Users are kept
in tremolo's database in `--data-dir`; without one they're lost on restart and a new admin is made
each time.

If every admin is locked out, start tremolo once with `--reset-admin` (the chart's
`auth.resetAdmin`). It makes the user `admin` an admin again, with `$TREMOLO_ADMIN_PASSWORD` or a
random password in the log, and ends its sessions. Remove the flag afterwards.

## Activity

**Users and teams** ends with the audit log: sign-ins (and failed ones), changes to users, teams
and tokens, and every acknowledge, take, forward, note, silence and maintenance window, with who
did it. Filter it by person. It's kept for `--history-retention` (a year by default).

Passwords are stored as bcrypt hashes, sessions and API tokens as SHA-256 hashes. A sign-in lasts
30 days; changing or resetting a password ends that user's sessions. Five wrong passwords in a
minute lock that name for a minute.

## Contacts

Each person sets on their profile how alerts reach them: a channel from the
[alerts config](alerts.md#people-and-teams), their address on it, and how long after an alert reaches
them to use it. For example: a push to their phone at once, a text message after 10 minutes.

## Teams

Admins make teams of users. An alert sent to a team reaches each member through their contacts, or,
if the team names its own channels or escalations, goes there instead. Name a team in a rule's
`notify`, a severity default or an escalation step, or forward an alert to it. **Mine** on the Alerts
page shows alerts assigned to you or your teams, or on their way to you.

## API tokens

For scripts, make a token on your profile and send it as `Authorization: Bearer tremolo_…`. A token
acts as you, with your role, and is shown only once.

Sign-in through an identity provider (OIDC) or a trusted proxy header is planned; local accounts
keep working alongside them.
