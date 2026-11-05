# Inspiration & references

wvf's feature set was surveyed against these tools and specs. The
minimal-local design goal: sitespeed.io's discipline + webvitals.dev's
debugging depth as a single self-hosted binary.

## Tools studied

- [webvitals.dev](https://webvitals.dev/) — CWV debugger UX; LCP/CLS/INP
  attribution shapes we mirror
- [sitespeed.io](https://sitespeed.io) — median-of-N discipline, coach
  findings, Browsertime HAR format (aggregates ALL iterations — divide
  before comparing)
- [Lighthouse](https://github.com/googlechrome/lighthouse) — audit IDs,
  category scores, opportunity diagnostics
- [PageSpeed Insights](https://developers.google.com/speed/docs/insights/v5/about) —
  CrUX field p75 + lab merge
- [WebPageTest](https://github.com/catchpoint/WebPageTest) — waterfall
  filmstrip, scripted interactions
- [GTmetrix](https://gtmetrix.com) — page-speed scoring model
- [lighthouse-ci](https://github.com/GoogleChrome/lighthouse-ci) —
  assertions-vs-baseline CI pattern
- [Lighthouse scorecalc](https://googlechrome.github.io/lighthouse/scorecalc/) —
  the green/amber/red band model our score pkg + badges follow

## Specs & libraries

- [web-vitals](https://github.com/GoogleChrome/web-vitals) (6.2.3 vendored)
- [web.dev vitals](https://web.dev/articles/vitals) ·
  [Lighthouse performance scoring](https://developer.chrome.com/docs/lighthouse/performance/performance-scoring) ·
  [Chrome performance insights](https://developer.chrome.com/docs/performance/insights)
- [chromedp](https://github.com/chromedp/chromedp) · [quic-go](https://github.com/quic-go/quic-go) ·
  [pquerna/cachecontrol](https://github.com/pquerna/cachecontrol)
- [shields.io](https://shields.io) / [badge-maker](https://www.npmjs.com/package/badge-maker)
  — our badge style + vendored generator
- [Bulma](https://bulma.io) · [Alpine](https://alpinejs.dev) · [Chart.js](https://www.chartjs.org)

See `matrix.md` for the capability comparison and `analysis.md` for the
design decisions.
