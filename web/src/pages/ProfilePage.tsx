import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { api, type Contact } from "../lib/api";
import { ago, goMinutes } from "../lib/format";
import { useAlerts, useMe } from "../lib/hooks";

const input = "rounded-md border border-line bg-bg px-2 py-1 text-sm";
const button = "rounded-md bg-accent px-3 py-1 text-sm font-medium text-white disabled:opacity-60 dark:text-bg";

export function ProfilePage() {
  const me = useMe();
  if (!me.data) return null;
  return (
    <div className="max-w-3xl space-y-8">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{me.data.displayName || me.data.name}</h1>
        <p className="text-sm text-muted">
          {me.data.name} · {me.data.role}
          {me.data.teams.length ? ` · ${me.data.teams.join(", ")}` : ""}
        </p>
      </div>
      <Contacts key={JSON.stringify(me.data.contacts)} initial={me.data.contacts ?? []} displayName={me.data.displayName ?? ""} />
      <Password />
      <Tokens />
    </div>
  );
}

/** Contacts is how alerts reach this person: each a channel, their address on it, and a delay. */
function Contacts({ initial, displayName }: { initial: Contact[]; displayName: string }) {
  const client = useQueryClient();
  const channels = useAlerts().data?.channels ?? [];
  const [name, setName] = useState(displayName);
  const [rows, setRows] = useState(initial.map((c) => ({ ...c, minutes: goMinutes(c.after) })));
  const save = useMutation({
    mutationFn: () =>
      api.updateMe({ displayName: name, contacts: rows.map((r) => ({ channel: r.channel, to: r.to.trim(), after: `${r.minutes || 0}m` })) }),
    onSuccess: (me) => client.setQueryData(["me"], me),
  });
  const set = (i: number, change: Partial<(typeof rows)[number]>) => setRows(rows.map((r, j) => (i === j ? { ...r, ...change } : r)));
  return (
    <section className="space-y-3">
      <h2 className="font-semibold">Profile and contacts</h2>
      <label className="flex items-center gap-3 text-sm">
        <span className="w-28 text-muted">Display name</span>
        <input value={name} onChange={(e) => setName(e.target.value)} maxLength={100} className={input} />
      </label>
      <p className="text-sm text-muted">
        When an alert goes to you, directly or through a team, it reaches you through each of these, after its delay, until someone acknowledges it.
        The address is yours on that channel: an email, a phone number, a Home Assistant notify service, an ntfy topic or a Slack member ID.
      </p>
      {channels.length === 0 && <p className="text-sm text-warn">There are no notification channels configured yet.</p>}
      <div className="space-y-2">
        {rows.map((r, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <select value={r.channel} onChange={(e) => set(i, { channel: e.target.value })} className={input} aria-label="Channel">
              {[...new Set([r.channel, ...channels])].map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
            <input value={r.to} onChange={(e) => set(i, { to: e.target.value })} placeholder="Your address on it" className={`${input} w-64`} />
            <span className="text-sm text-muted">after</span>
            <input
              type="number"
              min={0}
              value={r.minutes}
              onChange={(e) => set(i, { minutes: Number(e.target.value) })}
              className={`${input} w-20`}
              aria-label="Minutes"
            />
            <span className="text-sm text-muted">min</span>
            <button type="button" onClick={() => setRows(rows.filter((_, j) => j !== i))} className="text-muted hover:text-bad" title="Remove">
              <Trash2 className="size-4" />
            </button>
          </div>
        ))}
        {channels.length > 0 && (
          <button
            type="button"
            onClick={() => setRows([...rows, { channel: channels[0]!, to: "", after: "0s", minutes: rows.length ? 10 : 0 }])}
            className="inline-flex items-center gap-1 text-sm text-accent"
          >
            <Plus className="size-4" /> Add a contact
          </button>
        )}
      </div>
      <div className="flex items-center gap-3">
        <button type="button" className={button} disabled={save.isPending} onClick={() => save.mutate()}>
          Save
        </button>
        {save.isSuccess && <span className="text-sm text-good">Saved.</span>}
        {save.error && <span className="text-sm text-bad">{save.error.message}</span>}
      </div>
    </section>
  );
}

function Password() {
  const [old, setOld] = useState("");
  const [next, setNext] = useState("");
  const change = useMutation({
    mutationFn: () => api.changePassword(old, next),
    onSuccess: () => {
      setOld("");
      setNext("");
    },
  });
  return (
    <section className="space-y-3">
      <h2 className="font-semibold">Password</h2>
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          change.mutate();
        }}
      >
        <input
          type="password"
          autoComplete="current-password"
          placeholder="Current"
          value={old}
          onChange={(e) => setOld(e.target.value)}
          className={input}
        />
        <input
          type="password"
          autoComplete="new-password"
          placeholder="New, 8 or more characters"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          className={input}
        />
        <button type="submit" className={button} disabled={change.isPending || !old || next.length < 8}>
          Change
        </button>
        {change.isSuccess && <span className="text-sm text-good">Changed. Other sign-ins have ended.</span>}
        {change.error && <span className="text-sm text-bad">{change.error.message}</span>}
      </form>
    </section>
  );
}

/** Tokens are for scripts: send one as "Authorization: Bearer …". */
function Tokens() {
  const client = useQueryClient();
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: ({ signal }) => api.tokens(signal) });
  const [name, setName] = useState("");
  const [secret, setSecret] = useState("");
  const create = useMutation({
    mutationFn: () => api.createToken(name),
    onSuccess: (r) => {
      setSecret(r.secret);
      setName("");
      client.invalidateQueries({ queryKey: ["tokens"] });
    },
  });
  const remove = useMutation({ mutationFn: api.deleteToken, onSuccess: () => client.invalidateQueries({ queryKey: ["tokens"] }) });
  return (
    <section className="space-y-3">
      <h2 className="font-semibold">API tokens</h2>
      <p className="text-sm text-muted">
        For scripts and tools: send <code className="font-mono">Authorization: Bearer &lt;token&gt;</code>. A token can do what you can.
      </p>
      {secret && (
        <p className="rounded-md border border-accent/50 bg-accent/5 p-2 text-sm">
          Copy it now; it won't be shown again: <code className="font-mono break-all select-all">{secret}</code>
        </p>
      )}
      <ul className="divide-y divide-line/60 rounded-xl border border-line bg-panel">
        {(tokens.data ?? []).map((t) => (
          <li key={t.id} className="flex items-center gap-3 px-3 py-2 text-sm">
            <KeyRound className="size-4 text-muted" />
            <span className="font-medium">{t.name}</span>
            <span className="text-xs text-muted">
              made {ago(t.created)} ago{t.lastUsed ? `, used ${ago(t.lastUsed)} ago` : ", never used"}
            </span>
            <button type="button" className="ml-auto text-xs text-bad" onClick={() => remove.mutate(t.id)}>
              Delete
            </button>
          </li>
        ))}
        {tokens.data?.length === 0 && <li className="px-3 py-2 text-sm text-muted">No tokens.</li>}
      </ul>
      <form
        className="flex items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="What it's for" maxLength={100} className={input} />
        <button type="submit" className={button} disabled={!name.trim() || create.isPending}>
          Create token
        </button>
        {create.error && <span className="text-sm text-bad">{create.error.message}</span>}
      </form>
    </section>
  );
}
