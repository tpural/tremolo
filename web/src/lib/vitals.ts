/** Google's Web Vitals thresholds: good up to the first, poor above the second. */
export const VITALS: { name: string; label: string; unit: "ms" | ""; good: number; poor: number }[] = [
  { name: "lcp", label: "LCP", unit: "ms", good: 2500, poor: 4000 },
  { name: "inp", label: "INP", unit: "ms", good: 200, poor: 500 },
  { name: "cls", label: "CLS", unit: "", good: 0.1, poor: 0.25 },
];

export type Rating = "good" | "needs work" | "poor";

/** rate says how a vital's value does against its thresholds. */
export function rate(name: string, v: number): Rating {
  const t = VITALS.find((x) => x.name === name);
  if (!t || v <= t.good) return "good";
  return v <= t.poor ? "needs work" : "poor";
}

/** show writes a vital's value: seconds for long times, ms for short ones, plain for CLS. */
export function show(name: string, v: number) {
  if (name === "cls") return v.toFixed(2);
  return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${Math.round(v)}ms`;
}
