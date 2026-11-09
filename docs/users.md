# Users and teams

tremolo has its own accounts. Everyone signs in to see anything; what they can change depends on
their role.

| Role | Can |
|---|---|
| viewer | see everything |
| editor | also acknowledge, take, forward and annotate alerts, silence, set maintenance and run rules |
| admin | also manage users and teams |

## The first admin

A new tremolo has no users. The first person to open it creates the admin account, choosing its
name and password, and is signed in; after that the setup page is gone. Add everyone else under
**Users and teams**. No password is ever generated or written to a log.

Setup is open to whoever reaches a new tremolo first, so set it up before exposing it. To create the
admin without the setup page, for example in automation, set `$TREMOLO_ADMIN_PASSWORD` (the
chart's `auth.adminPasswordSecret`): the first start then creates a user named `admin` with that
password.

Users are kept in tremolo's database in `--data-dir`; without one they're lost on restart and
setup runs again.

If every admin is locked out, set `$TREMOLO_ADMIN_PASSWORD` and start tremolo once with
`--reset-admin` (the chart's `auth.resetAdmin` with `auth.adminPasswordSecret`). It makes the user
`admin` an admin again with that password, and ends its sessions. Remove the flag afterwards.

## Activity

**Users and teams** ends with the audit log: sign-ins (and failed ones), changes to users, teams
and tokens, and every acknowledge, take, forward, note, silence and maintenance window, with who
did it. Filter it by person. It's kept for `--history-retention` (a year by default).

Your profile lists where you're signed in (browser, address, when it was last used) and lets you
sign any of them out. Admins can sign a user out everywhere from **Users and teams**.

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
