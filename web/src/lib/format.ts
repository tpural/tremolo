const SI = ["", "K", "M", "G", "T", "P"];
const IEC = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];

function scaled(v: number, base: number, units: string[], sep: string): string {
  let i = 0;
  let n = Math.abs(v);
  while (n >= base && i < units.length - 1) {
    n /= base;
    i++;
  }
  const s = (Math.sign(v) * n).toFixed(n >= 100 || i === 0 ? 0 : 1);
  return units[i] ? `${s}${sep}${units[i]}` : s;
}

/** formatValue formats a number for a unit, short enough for a chart axis. */
export function formatValue(v: number | null | undefined, unit = "short"): string {
  if (v == null || Number.isNaN(v)) return "–";
  switch (unit) {
    case "percent":
      return `${v.toFixed(v >= 10 ? 0 : 1)}%`;
    case "percentunit":
      return `${(v * 100).toFixed(v >= 0.1 ? 0 : 1)}%`;
    case "bytes":
      return scaled(v, 1024, IEC, " ");
    case "bytes/s":
      return `${scaled(v, 1024, IEC, " ")}/s`;
    case "seconds":
      return duration(v);
    case "ms":
      return duration(v / 1000);
    case "celsius":
      return `${v.toFixed(0)}°C`;
    default: {
      if (Math.abs(v) < 1000) return Number.isInteger(v) ? String(v) : v.toFixed(Math.abs(v) < 1 ? 2 : 1);
      return scaled(v, 1000, SI, "");
    }
  }
}

/** duration formats seconds as the largest one or two units. */
export function duration(s: number): string {
  if (s < 0.001) return `${(s * 1e6).toFixed(0)}µs`;
  if (s < 1) return `${(s * 1000).toFixed(0)}ms`;
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m${Math.round(s % 60) ? ` ${Math.round(s % 60)}s` : ""}`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h${m % 60 ? ` ${m % 60}m` : ""}`;
  return `${Math.floor(h / 24)}d${h % 24 ? ` ${h % 24}h` : ""}`;
}

/** ago describes how long ago an ISO time was. */
export function ago(iso?: string, now = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t) || t <= 0) return "";
  return duration(Math.max(0, (now - t) / 1000));
}

/** legend fills a template like "{{pod}} on {{node}}" from series labels. */
export function legend(template: string | undefined, metric: Record<string, string>): string {
  if (!template) {
    const { __name__, ...rest } = metric;
    const parts = Object.entries(rest).map(([k, v]) => `${k}="${v}"`);
    return parts.length ? `${__name__ ?? ""}{${parts.join(", ")}}` : (__name__ ?? "value");
  }
  return template.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (_, k: string) => metric[k] ?? "");
}

/** substitute replaces $name variables in a query. Values are quoted-string safe. */
export function substitute(query: string, vars: Record<string, string>): string {
  return query.replace(/\$(\w+)/g, (m, k: string) => (k in vars ? vars[k].replace(/\\/g, "\\\\").replace(/"/g, '\\"') : m));
}

/** goMinutes reads a Go duration such as "1h10m0s" as whole minutes. */
export function goMinutes(d: string | undefined): number {
  let total = 0;
  for (const [, n, unit] of (d ?? "").matchAll(/([\d.]+)(h|ms|m|s|us|µs|ns)/g)) {
    total += Number(n) * ({ h: 60, m: 1, s: 1 / 60 } as Record<string, number>)[unit]! || 0;
  }
  return Math.round(total);
}
