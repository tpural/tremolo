import { useMutation, useQuery } from "@tanstack/react-query";
import { Check, Hand } from "lucide-react";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { api } from "../lib/api";
import { ago } from "../lib/format";
import { useMe } from "../lib/hooks";

const NAME_KEY = "tremolo-ack-name";

function readName() {
  try {
    return localStorage.getItem(NAME_KEY) ?? "";
  } catch {
    return "";
  }
}

function saveName(name: string) {
  try {
    localStorage.setItem(NAME_KEY, name);
  } catch {
    // private mode: not remembered
  }
}

/**
 * AckPage is where a notification's link lands. It works without signing in,
 * since the link is the credential, and asks before acknowledging so a chat
 * app's link preview can't.
 */
export function AckPage() {
  const { token = "" } = useParams();
  const me = useMe();
  const peek = useQuery({ queryKey: ["ack", token], queryFn: () => api.peekAck(token), retry: false });
  const [name, setName] = useState(readName);
  const ack = useMutation({ mutationFn: (by: string) => api.ackLink(token, by) });
  const signedIn = me.data?.name;
  const as = signedIn ?? peek.data?.person;
  const a = ack.data?.alert ?? peek.data?.alert;

  let body;
  if (peek.isLoading) body = <p className="text-sm text-muted">Loading…</p>;
  else if (!a) body = <p className="text-sm text-muted">That alert has resolved, or the link is old.</p>;
  else if (a.acked)
    body = (
      <p className="flex items-center gap-2 text-sm">
        <Check className="size-4 text-good" /> Acknowledged{a.ackedBy ? ` by ${a.ackedBy}` : ""} {ago(a.acked)} ago.
      </p>
    );
  else
    body = (
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (!as) saveName(name.trim());
          ack.mutate(as ? "" : name.trim());
        }}
      >
        <p className="text-sm text-muted">Escalation and repeats stop until it resolves, and everyone already told hears who has it.</p>
        {!as && (
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={100}
            placeholder="Your name (optional)"
            className="w-full rounded-md border border-line-strong/70 bg-panel px-2.5 py-1.5 text-sm"
          />
        )}
        <button
          type="submit"
          disabled={ack.isPending}
          className="inline-flex w-full items-center justify-center gap-1.5 rounded-md bg-accent px-3 py-2 text-sm font-medium text-on-accent disabled:opacity-60"
        >
          <Hand className="size-4" /> {ack.isPending ? "Acknowledging…" : as ? `Acknowledge as ${as}` : "Acknowledge"}
        </button>
        {ack.error && <p className="text-sm text-bad">{ack.error.message}</p>}
      </form>
    );

  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg px-4">
      <div className="w-full max-w-md space-y-4 rounded-2xl border border-line bg-panel p-6">
        <div className="flex items-center gap-2">
          <img src="/loon-calling.png" alt="" className="h-8" />
          <span className="text-xl font-semibold tracking-tight">tremolo</span>
        </div>
        {a && (
          <div>
            <p className="font-medium">{a.summary}</p>
            <p className="text-xs text-muted">
              {a.rule}
              {a.key ? ` / ${a.key}` : ""} · {a.severity} · firing for {ago(a.firingSince ?? a.since)}
            </p>
          </div>
        )}
        {body}
        <Link to={signedIn ? "/alerts" : "/login?next=/alerts"} className="block text-center text-xs text-accent">
          {signedIn ? "Open alerts" : "Sign in to forward it or see more"}
        </Link>
      </div>
    </div>
  );
}
