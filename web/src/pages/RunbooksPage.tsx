import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BookOpen, Check, Pencil, Plus, X } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { btn, card, Confirm, EmptyState, ErrorNote, field, message, PageHeader, pill, skeleton } from "../components/ui";
import { api, can, type Runbook, type RunbookProposal } from "../lib/api";
import { diffLines } from "../lib/diff";
import { ago } from "../lib/format";
import { useAlerts, useMe, useRunbookProposals } from "../lib/hooks";
import { Markdown } from "../lib/markdown";

/** RunbooksPage lists runbooks: how to handle each rule's alerts, and how they were fixed before. */
export function RunbooksPage() {
  const [params] = useSearchParams();
  const [q, setQ] = useState("");
  const list = useQuery({ queryKey: ["runbooks"], queryFn: ({ signal }) => api.runbooks(undefined, signal) });
  const me = useMe();
  const editor = can(me.data?.role, "editor");
  const rule = params.get("rule") ?? "";
  const words = q.trim().toLowerCase();
  const shown = (list.data ?? []).filter(
    (rb) => (!rule || rb.rules.includes(rule)) && (!words || `${rb.title} ${rb.rules.join(" ")} ${rb.body}`.toLowerCase().includes(words)),
  );
  return (
    <div className="space-y-6">
      <PageHeader
        title="Runbooks"
        sub={
          rule ? (
            <>
              for <span className="font-mono">{rule}</span>
            </>
          ) : (
            "How to handle each rule's alerts, and how they were fixed before"
          )
        }
        actions={
          editor && (
            <Link to={`/runbooks/new${rule ? `?rule=${encodeURIComponent(rule)}` : ""}`} className={`${btn.base} ${btn.primary} ${btn.sm}`}>
              <Plus className="size-4" /> New runbook
            </Link>
          )
        }
      />
      <input
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Search titles, rules and text"
        className={`${field} w-full max-w-md`}
        aria-label="Search runbooks"
      />
      <Proposals editor={editor} />
      {list.error && <ErrorNote error={list.error} />}
      {list.isLoading ? (
        <div className={`${skeleton} h-32`} />
      ) : shown.length ? (
        <ul className={`${card} divide-y divide-line/60`}>
          {shown.map((rb) => (
            <li key={rb.id}>
              <Link to={`/runbooks/${rb.id}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5 text-sm hover:bg-panel-2/60">
                <BookOpen className="size-4 text-muted" />
                <span className="font-medium">{rb.title}</span>
                {rb.rules.map((r) => (
                  <span key={r} className={`${pill} border-line font-mono text-muted`}>
                    {r}
                  </span>
                ))}
                <span className="ml-auto text-xs text-muted">
                  {fixes(rb.body)} · edited {ago(rb.updated)} ago by {rb.updatedBy}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      ) : (
        <div className={card}>
          <EmptyState>
            {words || rule
              ? "No runbooks match."
              : "No runbooks yet. Write one for a rule, or note how an alert was fixed from its entry in the alert history."}
          </EmptyState>
        </div>
      )}
    </div>
  );
}

/** Proposals are agents' runbook edits, waiting for a person to approve or reject them. */
function Proposals({ editor }: { editor: boolean }) {
  const list = useRunbookProposals();
  if (!list.data?.length) return null;
  return (
    <section className="space-y-3">
      <h2 className="text-sm font-semibold">
        Waiting for approval <span className="font-normal text-muted">· agents' edits change nothing until a person approves them</span>
      </h2>
      {list.data.map((p) => (
        <ProposalCard key={p.id} p={p} editor={editor} />
      ))}
    </section>
  );
}

function ProposalCard({ p, editor }: { p: RunbookProposal; editor: boolean }) {
  const client = useQueryClient();
  const current = useQuery({
    queryKey: ["runbook", p.runbook.id],
    queryFn: ({ signal }) => api.runbook(p.runbook.id, signal),
    enabled: p.base > 0,
  });
  const decide = useMutation({
    mutationFn: (d: "approve" | "reject") => api.decideRunbookProposal(p.id, d),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["runbook-proposals"] });
      void client.invalidateQueries({ queryKey: ["runbooks"] });
      void client.invalidateQueries({ queryKey: ["runbook", p.runbook.id] });
    },
  });
  const before = p.base > 0 ? (current.data?.body ?? "") : "";
  const stale = p.base > 0 && current.data && current.data.version !== p.base;
  const lines = diffLines(before, p.runbook.body);
  return (
    <div className={`${card} space-y-3 p-3`}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        <span className="font-medium">{p.runbook.title}</span>
        <span className={`${pill} border-line text-muted`}>{p.base ? `edit of version ${p.base}` : "new runbook"}</span>
        {p.runbook.rules.map((r) => (
          <span key={r} className={`${pill} border-line font-mono text-muted`}>
            {r}
          </span>
        ))}
        <span className="text-xs text-muted">
          proposed by {p.by} {ago(p.created)} ago
        </span>
        {editor && (
          <span className="ml-auto flex gap-2">
            <button
              type="button"
              disabled={decide.isPending}
              onClick={() => decide.mutate("reject")}
              className={`${btn.base} ${btn.danger} ${btn.sm}`}
            >
              <X className="size-4" /> Reject
            </button>
            <button
              type="button"
              disabled={decide.isPending || !!stale}
              onClick={() => decide.mutate("approve")}
              className={`${btn.base} ${btn.primary} ${btn.sm}`}
            >
              <Check className="size-4" /> Approve
            </button>
          </span>
        )}
      </div>
      {stale && <p className="text-xs text-warn">The runbook changed since this was proposed; reject it and let the agent propose again.</p>}
      {decide.error && <ErrorNote error={decide.error} />}
      {current.isLoading ? (
        <div className={`${skeleton} h-24`} />
      ) : (
        <pre className="max-h-96 overflow-auto rounded-md bg-panel-2/60 p-2 font-mono text-xs leading-5">
          {lines.map((l, i) => (
            <div
              key={i}
              className={l.op === "+" ? "bg-good/15 text-fg" : l.op === "-" ? "bg-bad/15 text-muted line-through" : "text-muted"}
            >
              {l.op} {l.text}
            </div>
          ))}
        </pre>
      )}
    </div>
  );
}

/** fixes counts the dated fix entries in a runbook body. */
function fixes(body: string) {
  const n = (body.match(/^## \d{4}-\d{2}-\d{2}:/gm) ?? []).length;
  return n === 1 ? "1 fix" : `${n} fixes`;
}

/** RunbookPage shows one runbook, or edits it; /runbooks/new makes one. */
export function RunbookPage() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const isNew = id === "new";
  const rb = useQuery({ queryKey: ["runbook", id], queryFn: ({ signal }) => api.runbook(id, signal), enabled: !isNew });
  const me = useMe();
  const editor = can(me.data?.role, "editor");
  const [editing, setEditing] = useState(false);

  if (isNew) {
    const rule = params.get("rule") ?? "";
    const blank: Runbook = {
      id: "",
      title: rule,
      body: rule ? `# ${rule}\n\nHow to handle \`${rule}\` alerts.\n` : "",
      rules: rule ? [rule] : [],
      version: 0,
      updated: "",
      updatedBy: "",
    };
    return <Editor start={blank} />;
  }
  if (rb.error) return <ErrorNote error={rb.error} />;
  if (!rb.data) return <div className={`${skeleton} h-64`} />;
  if (editing) return <Editor start={rb.data} onDone={() => setEditing(false)} />;
  const r = rb.data;
  return (
    <div className="space-y-6">
      <PageHeader
        back={{ to: "/runbooks", label: "Runbooks" }}
        title={r.title}
        sub={`version ${r.version}, edited ${ago(r.updated)} ago by ${r.updatedBy}`}
        actions={
          editor && (
            <button type="button" onClick={() => setEditing(true)} className={`${btn.base} ${btn.secondary} ${btn.sm}`}>
              <Pencil className="size-4" /> Edit
            </button>
          )
        }
      />
      {r.rules.length > 0 && (
        <p className="flex flex-wrap items-center gap-2 text-sm text-muted">
          For alerts from
          {r.rules.map((rule) => (
            <Link key={rule} to={`/rules/${encodeURIComponent(rule)}`} className={`${pill} border-line font-mono hover:border-accent`}>
              {rule}
            </Link>
          ))}
        </p>
      )}
      {(r.fixes?.length ?? 0) > 0 && (
        <section className="space-y-2">
          <h2 className="text-base font-semibold">What has worked</h2>
          <ul className={`${card} divide-y divide-line/60 text-sm`}>
            {r.fixes!.map((f) => (
              <li key={f.text} className="flex flex-wrap items-baseline gap-x-3 px-3 py-2">
                <span className="min-w-0 flex-1">{f.text}</span>
                <span className="text-xs text-good">held {f.held}</span>
                <span className={`text-xs ${f.recurred ? "text-bad" : "text-muted"}`}>came back {f.recurred}</span>
                {f.pending > 0 && <span className="text-xs text-muted">{f.pending} waiting</span>}
                <span className="text-xs text-muted">{ago(f.last)} ago</span>
              </li>
            ))}
          </ul>
          <p className="text-xs text-muted">From agents' investigations: a fix held when its alert stayed away for a day.</p>
        </section>
      )}
      <article className={`${card} p-4`}>
        <Markdown text={r.body} />
      </article>
      <Versions id={r.id} />
      <p className="text-xs text-muted">
        As plain Markdown, for scripts and assistants:{" "}
        <a className="font-mono text-accent" href={`/api/runbooks/${r.id}/markdown`}>
          /api/runbooks/{r.id}/markdown
        </a>
      </p>
    </div>
  );
}

function Versions({ id }: { id: string }) {
  const [open, setOpen] = useState(false);
  const vs = useQuery({ queryKey: ["runbook-versions", id], queryFn: ({ signal }) => api.runbookVersions(id, signal), enabled: open });
  const [shown, setShown] = useState<Runbook>();
  return (
    <section className="space-y-2">
      <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="text-sm text-accent">
        {open ? "Hide history" : "History"}
      </button>
      {open && (
        <ul className={`${card} divide-y divide-line/60 text-sm`}>
          {(vs.data ?? []).map((v) => (
            <li key={v.version} className="px-3 py-2">
              <button
                type="button"
                className="flex w-full items-center gap-3 text-left"
                onClick={() => setShown(shown?.version === v.version ? undefined : v)}
              >
                <span className="font-mono text-xs text-muted">v{v.version}</span>
                <span>{v.updatedBy}</span>
                <span className="ml-auto text-xs text-muted">{new Date(v.updated).toLocaleString()}</span>
              </button>
              {shown?.version === v.version && (
                <pre className="mt-2 max-h-80 overflow-auto rounded-lg bg-panel-2 p-3 font-mono text-xs whitespace-pre-wrap">{v.body}</pre>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Editor({ start, onDone }: { start: Runbook; onDone?: () => void }) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const isNew = start.version === 0;
  const [rb, setRb] = useState(start);
  const [rules, setRules] = useState(start.rules.join(", "));
  const [preview, setPreview] = useState(false);
  const known = useAlerts();
  const parsed = rules
    .split(",")
    .map((r) => r.trim())
    .filter(Boolean);
  const unknown = parsed.filter((r) => known.data && !known.data.rules?.some((k) => k.name === r));
  const save = useMutation({
    mutationFn: () => (isNew ? api.createRunbook({ title: rb.title, body: rb.body, rules: parsed }) : api.saveRunbook({ ...rb, rules: parsed })),
    onSuccess: (saved) => {
      void client.invalidateQueries({ queryKey: ["runbooks"] });
      client.setQueryData(["runbook", saved.id], saved);
      void client.invalidateQueries({ queryKey: ["alerts"] });
      if (isNew) navigate(`/runbooks/${saved.id}`);
      else onDone?.();
    },
  });
  const del = useMutation({
    mutationFn: () => api.deleteRunbook(rb.id),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ["runbooks"] });
      void client.invalidateQueries({ queryKey: ["alerts"] });
      navigate("/runbooks");
    },
  });
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <PageHeader
        back={{ to: isNew ? "/runbooks" : `/runbooks/${rb.id}`, label: isNew ? "Runbooks" : rb.title }}
        title={isNew ? "New runbook" : "Edit runbook"}
      />
      <label className="block space-y-1 text-sm">
        <span className="text-muted">Title</span>
        <input required maxLength={200} value={rb.title} onChange={(e) => setRb({ ...rb, title: e.target.value })} className={`${field} w-full`} />
      </label>
      <label className="block space-y-1 text-sm">
        <span className="text-muted">Rules it covers, comma-separated</span>
        <input
          value={rules}
          onChange={(e) => setRules(e.target.value)}
          placeholder="disk-full, pvc-filling"
          className={`${field} w-full font-mono`}
        />
        {unknown.length > 0 && <span className="text-xs text-warn-ink">No rule named {unknown.join(", ")} right now.</span>}
      </label>
      <div className="space-y-1 text-sm">
        <div className="flex items-center gap-3">
          <span className="text-muted">Markdown</span>
          <button type="button" onClick={() => setPreview(!preview)} aria-pressed={preview} className="ml-auto text-xs text-accent">
            {preview ? "Edit" : "Preview"}
          </button>
        </div>
        {preview ? (
          <div className={`${card} min-h-64 p-4`}>
            <Markdown text={rb.body} />
          </div>
        ) : (
          <textarea
            value={rb.body}
            onChange={(e) => setRb({ ...rb, body: e.target.value })}
            rows={Math.min(40, Math.max(14, rb.body.split("\n").length + 2))}
            spellCheck
            className={`${field} h-auto w-full py-2 font-mono text-xs leading-5`}
            aria-label="Runbook text"
          />
        )}
      </div>
      {(save.error || del.error) && <ErrorNote error={save.error ?? del.error} />}
      <div className="flex flex-wrap items-center gap-2">
        <button type="submit" disabled={save.isPending} className={`${btn.base} ${btn.primary}`}>
          {save.isPending ? "Saving…" : isNew ? "Create" : "Save"}
        </button>
        <button type="button" onClick={() => (isNew ? navigate("/runbooks") : onDone?.())} className={`${btn.base} ${btn.quiet}`}>
          Cancel
        </button>
        {!isNew && (
          <span className="ml-auto">
            <Confirm label="Delete" ask="Delete it and its history?" onConfirm={() => del.mutate()} disabled={del.isPending} />
          </span>
        )}
      </div>
      {save.error && message(save.error).includes("changed") && (
        <p className="text-xs text-muted">Someone saved it since you opened it; copy your text, reload, and apply it again.</p>
      )}
    </form>
  );
}
