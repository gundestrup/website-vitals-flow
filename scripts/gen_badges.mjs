// Offline badge generation: emits vendored shields-format SVGs.
// Run via `npm run gen:badges` (dev-time only; output is vendored).
import { makeBadge } from "badge-maker";
import { writeFileSync } from "fs";

const metrics = ["lcp","fcp","cls","ttfb","tbt","inp","proto","comp","cache","tls"];
const bands = { good: "#2e7d32", ok: "#f9a825", poor: "#c62828", na: "#97a5a0" };
const out = "internal/server/static/badges/";

for (const m of metrics) {
  for (const [band, color] of Object.entries(bands)) {
    const svg = makeBadge({
      label: m.toUpperCase(), message: band, color,
      style: "flat",
    }).replace(/<(rect[^>]*)fill="#[^"]*"(>\s*<rect[^>]*?)?/,
      // make right-segment color CSS-driven for theme overrides
      (s) => s.replace(`fill="${color}"`, `fill="var(--band-${band},${color})"`));
    writeFileSync(`${out}${m}-${band}.svg`, svg);
  }
}
console.log(`wrote ${metrics.length * Object.keys(bands).length} badges`);
