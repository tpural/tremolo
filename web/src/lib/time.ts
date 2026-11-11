export const RANGES = [
  { label: "15m", seconds: 15 * 60 },
  { label: "1h", seconds: 3600 },
  { label: "6h", seconds: 6 * 3600 },
  { label: "24h", seconds: 24 * 3600 },
  { label: "7d", seconds: 7 * 24 * 3600 },
  { label: "30d", seconds: 30 * 24 * 3600 },
  { label: "90d", seconds: 90 * 24 * 3600 },
  { label: "1y", seconds: 365 * 24 * 3600 },
] as const;

export type RangeLabel = (typeof RANGES)[number]["label"];

export function rangeSeconds(label: string): number {
  return RANGES.find((r) => r.label === label)?.seconds ?? 3600;
}

/** LONG is past where queries get slow: the picker warns, and a timeout offers 7 days. */
export const LONG = 31 * 24 * 3600;

/** lookBack writes a range in seconds as days, e.g. "7 days". */
export function lookBack(seconds: number): string {
  const d = Math.round(seconds / 86400);
  if (d >= 365 && d % 365 === 0) return d === 365 ? "1 year" : `${d / 365} years`;
  return d === 1 ? "1 day" : `${d} days`;
}

/** step picks a query resolution giving about 240 points, rounded to a nice interval. */
export function step(seconds: number): number {
  const raw = seconds / 240;
  const nice = [15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400];
  return nice.find((n) => n >= raw) ?? 172800;
}
