import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LogOut, Trash2, UserPlus, Users } from "lucide-react";
import { useState } from "react";
import { api, type Role, type Team, type User } from "../lib/api";
import { useAlerts, useMe } from "../lib/hooks";
import { btn, card, Confirm, EmptyState, ErrorNote, field, PageHeader, SectionHeader, skeleton } from "../components/ui";

const input = field;
const button = `${btn.base} ${btn.primary}`;
const ROLES: { role: Role; about: string }[] = [
  { role: "viewer", about: "sees everything" },
  { role: "editor", about: "also triages alerts, silences and sets maintenance" },
  { role: "admin", about: "also manages users and teams" },
];

/** UsersPage is for admins: accounts, their roles, teams, and what everyone did. */
export function UsersPage() {
  const users = useQuery({ queryKey: ["users"], queryFn: ({ signal }) => api.users(signal) });
  const teams = useQuery({ queryKey: ["teams"], queryFn: ({ signal }) => api.teams(signal) });
  return (
    <div className="max-w-4xl space-y-8">
      <PageHeader title="Users and teams" sub="Who can sign in, what they can do, and who alerts reach" />
      <section className="space-y-3">
        <SectionHeader title="Users" />
        {users.error && <ErrorNote error={users.error} />}
        {users.isLoading ? (
          <div className={`${skeleton} h-24`} />
        ) : (
          <ul className={`divide-y divide-line/60 ${card}`}>
            {(users.data ?? []).map((u) => (
              <UserRow key={u.name} user={u} />
            ))}
          </ul>
        )}
        <NewUser />
        <p className="text-xs text-muted">{ROLES.map((r) => `${r.role}: ${r.about}`).join(" · ")}</p>
      </section>
      <section className="space-y-3">
        <SectionHeader title="Teams" />
        <p className="text-sm text-muted">
          Send alerts to a team by naming it in a rule's <code className="font-mono">notify</code>, in an escalation, or by forwarding. A team reaches
          each member through their own contacts, or, if you set it, through its own channels and escalations.
        </p>
        <div className="space-y-2">
          {(teams.data ?? []).map((t) => (
            <TeamRow key={t.name} team={t} users={users.data ?? []} />
          ))}
          <TeamRow users={users.data ?? []} />
        </div>
      </section>
      <Activity />
    </div>
  );
}

/** Activity is the audit log: sign-ins, changes to users and teams, triage, silences and maintenance. */
function Activity() {
  const [actor, setActor] = useState("");
  const [via, setVia] = useState("");
  const log = useInfiniteQuery({
    queryKey: ["audit", actor, via],
    queryFn: ({ pageParam, signal }) => api.audit({ actor, via: via.trim(), before: pageParam }, signal),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => (last.length === 100 ? last[last.length - 1].id : undefined),
  });
  const users = useQuery({ queryKey: ["users"], queryFn: ({ signal }) => api.users(signal) });
  const rows = log.data?.pages.flat() ?? [];
  return (
    <section className="space-y-3">
      <SectionHeader
        title="Activity"
        actions={
          <div className="flex flex-wrap gap-2">
            <select value={actor} onChange={(e) => setActor(e.target.value)} className={input} aria-label="Whose activity">
              <option value="">Everyone</option>
              {(users.data ?? []).map((u) => (
                <option key={u.name} value={u.name}>
                  {u.displayName || u.name}
                </option>
              ))}
            </select>
            <input value={via} onChange={(e) => setVia(e.target.value)} placeholder="Via token" aria-label="Via token" className={`${input} w-32`} />
          </div>
        }
      />
      <ul className={`divide-y divide-line/60 ${card}`}>
        {rows.map((e) => (
          <li key={e.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-3 py-1.5 text-sm">
            <span className="w-40 shrink-0 font-mono text-xs text-muted">{new Date(e.time).toLocaleString()}</span>
            <span className={e.action === "sign-in failed" ? "text-bad" : ""}>
              <span className="font-medium">{e.actor || "someone"}</span> {e.action}
              {e.target && <span className="font-mono text-xs"> {e.target}</span>}
              {e.via && <span className="text-xs text-muted"> via token {e.via}</span>}
            </span>
            {e.detail && <span className="text-xs text-muted">{e.detail}</span>}
          </li>
        ))}
        {!rows.length &&
          (log.isLoading ? (
            <li className={`${skeleton} m-2 h-16`} />
          ) : (
            <li>
              <EmptyState compact>Nothing yet.</EmptyState>
            </li>
          ))}
      </ul>
      {log.hasNextPage && (
        <button type="button" onClick={() => log.fetchNextPage()} disabled={log.isFetchingNextPage} className="text-sm text-accent">
          {log.isFetchingNextPage ? "Loading…" : "Older"}
        </button>
      )}
    </section>
  );
}

function useInvalidate(...keys: string[]) {
  const client = useQueryClient();
  return () => keys.forEach((k) => client.invalidateQueries({ queryKey: [k] }));
}

function UserRow({ user: u }: { user: User }) {
  const me = useMe();
  const refresh = useInvalidate("users", "alerts");
  const [password, setPassword] = useState("");
  const update = useMutation({
    mutationFn: (change: { role?: Role; password?: string }) => api.updateUser(u.name, change),
    onSuccess: () => {
      setPassword("");
      refresh();
    },
  });
  const remove = useMutation({ mutationFn: () => api.deleteUser(u.name), onSuccess: refresh });
  const signOut = useMutation({ mutationFn: () => api.endSessions(u.name) });
  const self = me.data?.name === u.name;
  return (
    <li className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
      <span className="font-medium">{u.displayName || u.name}</span>
      {u.displayName && <span className="font-mono text-xs text-muted">{u.name}</span>}
      <span className="text-xs text-muted">
        {(u.contacts ?? []).length} contact{(u.contacts ?? []).length === 1 ? "" : "s"}
      </span>
      <select
        value={u.role}
        disabled={update.isPending}
        onChange={(e) => update.mutate({ role: e.target.value as Role })}
        className={`${input} ml-auto`}
        aria-label="Role"
      >
        {ROLES.map((r) => (
          <option key={r.role}>{r.role}</option>
        ))}
      </select>
      <form
        className="flex items-center gap-1"
        onSubmit={(e) => {
          e.preventDefault();
          update.mutate({ password });
        }}
      >
        <input
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="New password"
          className={`${input} w-36`}
        />
        <button type="submit" disabled={password.length < 8} className="text-xs text-accent disabled:opacity-50">
          Set
        </button>
      </form>
      {!self && (
        <button
          type="button"
          onClick={() => signOut.mutate()}
          disabled={signOut.isPending}
          className="text-muted hover:text-fg"
          title={`Sign ${u.name} out everywhere`}
          aria-label={`Sign ${u.name} out everywhere`}
        >
          <LogOut className="size-4" />
        </button>
      )}
      {!self && (
        <Confirm label={<Trash2 className="size-4" aria-label={`Delete ${u.name}`} />} ask={`Delete ${u.name}?`} onConfirm={() => remove.mutate()} />
      )}
      {update.isSuccess && update.variables?.password && <span className="w-full text-xs text-good">Password set; their sign-ins have ended.</span>}
      {signOut.isSuccess && <span className="w-full text-xs text-good">Signed out everywhere.</span>}
      {(update.error ?? remove.error) && <span className="w-full text-xs text-bad">{(update.error ?? remove.error)?.message}</span>}
    </li>
  );
}

function NewUser() {
  const refresh = useInvalidate("users", "alerts");
  const [form, setForm] = useState({ name: "", displayName: "", role: "editor" as Role, password: "" });
  const create = useMutation({
    mutationFn: () => api.createUser(form),
    onSuccess: () => {
      setForm({ name: "", displayName: "", role: "editor", password: "" });
      refresh();
    },
  });
  return (
    <form
      className="flex flex-wrap items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <UserPlus className="size-4 text-muted" />
      <input
        value={form.name}
        onChange={(e) => setForm({ ...form, name: e.target.value.toLowerCase() })}
        placeholder="name"
        autoCapitalize="none"
        className={`${input} w-32`}
      />
      <input
        value={form.displayName}
        onChange={(e) => setForm({ ...form, displayName: e.target.value })}
        placeholder="Display name"
        className={input}
      />
      <select value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value as Role })} className={input} aria-label="Role">
        {ROLES.map((r) => (
          <option key={r.role}>{r.role}</option>
        ))}
      </select>
      <input
        type="password"
        autoComplete="new-password"
        value={form.password}
        onChange={(e) => setForm({ ...form, password: e.target.value })}
        placeholder="First password"
        className={`${input} w-40`}
      />
      <button type="submit" className={button} disabled={!form.name || form.password.length < 8 || create.isPending}>
        Add user
      </button>
      {create.error && <span className="w-full text-sm text-bad">{create.error.message}</span>}
    </form>
  );
}

/** TeamRow edits a team, or makes a new one when there's no team. */
function TeamRow({ team, users }: { team?: Team; users: User[] }) {
  const refresh = useInvalidate("teams", "alerts", "me");
  const targets = useAlerts().data;
  const [name, setName] = useState(team?.name ?? "");
  const [members, setMembers] = useState<string[]>(team?.members ?? []);
  const [notify, setNotify] = useState((team?.notify ?? []).join(", "));
  const save = useMutation({
    mutationFn: () =>
      api.saveTeam({
        name,
        members,
        notify: notify
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
      }),
    onSuccess: () => {
      if (!team) {
        setName("");
        setMembers([]);
        setNotify("");
      }
      refresh();
    },
  });
  const remove = useMutation({ mutationFn: () => api.deleteTeam(team!.name), onSuccess: refresh });
  const dirty = !team || name !== team.name || members.join() !== team.members.join() || notify !== (team.notify ?? []).join(", ");
  return (
    <form
      className="space-y-2 rounded-xl border border-line bg-panel p-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Users className="size-4 text-muted" />
        {team ? (
          <span className="font-medium">{team.name}</span>
        ) : (
          <input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="New team name" className={`${input} w-40`} />
        )}
        <input
          value={notify}
          onChange={(e) => setNotify(e.target.value)}
          placeholder="Own channels or escalations (optional)"
          list="notify-targets"
          className={`${input} min-w-64 flex-1`}
        />
        <datalist id="notify-targets">
          {[...(targets?.channels ?? []), ...(targets?.escalations ?? [])].map((t) => (
            <option key={t} value={t} />
          ))}
        </datalist>
        <button type="submit" className={button} disabled={!name || !dirty || save.isPending}>
          {team ? "Save" : "Add team"}
        </button>
        {team && (
          <Confirm
            label={<Trash2 className="size-4" aria-label={`Delete ${team.name}`} />}
            ask={`Delete ${team.name}?`}
            onConfirm={() => remove.mutate()}
          />
        )}
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 pl-6 text-sm">
        {users.map((u) => (
          <label key={u.name} className="inline-flex items-center gap-1.5">
            <input
              type="checkbox"
              checked={members.includes(u.name)}
              onChange={(e) => setMembers(e.target.checked ? [...members, u.name].sort() : members.filter((m) => m !== u.name))}
            />
            {u.displayName || u.name}
          </label>
        ))}
      </div>
      {(save.error ?? remove.error) && <p className="text-xs text-bad">{(save.error ?? remove.error)?.message}</p>}
    </form>
  );
}
