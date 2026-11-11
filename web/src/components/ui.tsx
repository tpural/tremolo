import { ArrowLeft } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router";
import { withRange } from "../lib/time";

/** Shared look for controls, so every page's buttons, fields and badges match. */

export const btn = {
  base: "inline-flex h-8 items-center justify-center gap-1.5 rounded-md px-3 text-sm font-medium whitespace-nowrap transition-colors disabled:pointer-events-none disabled:opacity-60",
  primary: "bg-accent text-on-accent hover:bg-accent/90",
  secondary: "border border-line-strong/60 bg-panel text-fg hover:border-accent hover:bg-panel-2",
  quiet: "text-muted hover:bg-panel-2 hover:text-fg",
  danger: "text-bad hover:bg-bad/10",
  sm: "h-7 gap-1 px-2 text-xs",
  icon: "size-8 px-0",
};

/** button joins a variant with the base, and sm when asked. */
export function button(variant: "primary" | "secondary" | "quiet" | "danger", size?: "sm" | "icon") {
  return [btn.base, btn[variant], size ? btn[size] : ""].join(" ");
}

export const field =
  "h-8 rounded-md border border-line-strong/70 bg-panel px-2.5 text-sm text-fg placeholder:text-muted focus:border-accent focus:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:opacity-60";
export const fieldSm = "h-7 px-2 text-xs";

export const seg = {
  wrap: "inline-flex rounded-lg border border-line bg-panel p-0.5",
  item: "rounded-md px-2.5 py-1 text-xs text-muted hover:text-fg aria-pressed:bg-panel-2 aria-pressed:font-medium aria-pressed:text-fg",
};

export const card = "rounded-xl border border-line bg-panel";
export const row = "flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm";

export const callout = {
  base: "rounded-2xl border p-4",
  bad: "border-bad/40 bg-bad/5",
  warn: "border-warn/40 bg-warn/5",
  calm: "border-line bg-panel",
};

export const badge = "inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-semibold uppercase tracking-wide";
export const badgeTone = {
  bad: "bg-bad/12 text-bad",
  warn: "bg-warn/12 text-warn-ink",
  good: "bg-good/12 text-good-ink",
  info: "bg-accent/12 text-accent",
  sleep: "bg-sleep/12 text-sleep",
  neutral: "border border-line text-muted",
};

export const pill = "inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs";
export const skeleton = "animate-pulse rounded-lg bg-panel-2";

/** PageHeader is a page's title, a line about it, an optional way back, and its actions. */
export function PageHeader({
  title,
  sub,
  back,
  actions,
  mono,
}: {
  title: ReactNode;
  sub?: ReactNode;
  back?: { to: string; label: string };
  actions?: ReactNode;
  mono?: boolean;
}) {
  const [params] = useSearchParams();
  return (
    <div className="space-y-1">
      {back && (
        <Link to={withRange(back.to, params.get("range"))} className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
          <ArrowLeft className="size-4" /> {back.label}
        </Link>
      )}
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h1 className={`text-2xl font-semibold tracking-tight ${mono ? "font-mono" : ""}`}>{title}</h1>
        {sub && <div className="text-sm text-muted">{sub}</div>}
        {actions && <div className="ml-auto flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </div>
  );
}

/** SectionHeader titles a part of a page, with its actions on the right. */
export function SectionHeader({ title, actions }: { title: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <h2 className="text-base font-semibold">{title}</h2>
      {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
    </div>
  );
}

/** EmptyState says there's nothing here, with the quiet loon unless compact. */
export function EmptyState({ children, action, compact }: { children: ReactNode; action?: ReactNode; compact?: boolean }) {
  return (
    <div className={`flex flex-col items-center gap-2 text-center text-sm text-muted ${compact ? "py-6" : "py-12"}`}>
      {!compact && <img src="/loon-quiet.png" alt="" className="w-16 opacity-60" />}
      <div>{children}</div>
      {action}
    </div>
  );
}

/** message is an error's text, without the "Error: " String() adds. */
export function message(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

/** ErrorNote shows an error's message, never "Error: …". */
export function ErrorNote({ error }: { error: unknown }) {
  return (
    <p role="alert" className="rounded-lg border border-bad/40 bg-bad/5 px-3 py-2 text-sm text-bad">
      {message(error)} <Narrower error={error} />
    </p>
  );
}

/** Narrower offers the last 7 days when a query over a longer range ran out of time. */
export function Narrower({ error }: { error: unknown }) {
  const [params, setParams] = useSearchParams();
  const range = params.get("range") ?? "";
  if (!message(error).includes("took longer than") || !["30d", "90d", "1y"].includes(range)) return null;
  return (
    <button
      type="button"
      className="font-medium underline underline-offset-2"
      onClick={() =>
        setParams(
          (p) => {
            p.set("range", "7d");
            return p;
          },
          { replace: true },
        )
      }
    >
      Try the last 7 days
    </button>
  );
}

/** Confirm is a delete that asks first: the first click says what will happen, the second does it. */
export function Confirm({
  label,
  ask,
  onConfirm,
  disabled,
  icon,
}: {
  label: ReactNode;
  ask: ReactNode;
  onConfirm: () => void;
  disabled?: boolean;
  icon?: ReactNode;
}) {
  const [asking, setAsking] = useState(false);
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={() => (asking ? onConfirm() : setAsking(true))}
      onBlur={() => setAsking(false)}
      className={`${btn.base} ${btn.sm} ${asking ? "bg-bad/10 text-bad" : btn.quiet}`}
    >
      {icon}
      {asking ? ask : label}
    </button>
  );
}
