# Session replay: size and upload-CPU benchmark (2026-10-08)

Throwaway. Reproduce: `bun install && bun run record.ts 90` (Part A, writes `out/`, git-ignored) and
`go test -run x -bench . -benchmem -cpu 1 -count 3 -benchtime 3s` (Part B).

## Setup

- rrweb **2.1.7** (UMD build injected after `load`), Playwright 1.64, headless Chromium, 1280×800, bypassCSP.
- Pages (live, fetched 2026-10-08): Wikipedia "Web analytics", MDN "Using Fetch", github.com/explore.
- ~91 s scripted activity per page: mouse wander, wheel scrolls (incl. scroll-ups), clicks every ~3 s
  (links neutralised so the page stays put), typing into an injected text input every ~6 iterations,
  a fixed widget that mutates a 10-item list every 2 s, a password input.
- Configs: `defaults` = `record({emit})`; `lean` = sampling `{mousemove:100, scroll:150, input:'last'}`,
  recordCanvas/inlineImages/collectFonts false, `maskInputOptions:{password:true}`, checkoutEveryNms 300000;
  `lean-nomouse` = lean with `mousemove:false`.
- gzip = Node zlib level 6. "10 s chunks" = events batched per 10 s and gzipped separately (like a browser
  uploading every 10 s via CompressionStream).

## Part A: sizes (measured, 91 s per run)

| page | config | DOM nodes | events | raw KB | gzip KB | 10 s-chunked gzip KB | 1st full snapshot raw / gz KB | incremental gz KB/min |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| wikipedia | defaults | 2131 | 544 | 933 | 130 | 132 | 823 / 115 | 9.5 |
| wikipedia | lean | 2131 | 362 | 897 | 126 | 129 | 823 / 115 | 7.3 |
| wikipedia | lean-nomouse | 2131 | 259 | 881 | 123 | 126 | 823 / 115 | 5.3 |
| mdn | defaults | 1043 | 565 | 832 | 97 | 99 | 692 / 80 | 11.0 |
| mdn | lean | 1043 | 392 | 797 | 93 | 96 | 692 / 80 | 8.8 |
| mdn | lean-nomouse | 1043 | 285 | 780 | 90 | 92 | 692 / 80 | 6.8 |
| github | defaults | 1381 | 494 | 4539 | 574 | 576 | 4456 / 564 | 6.1 |
| github | lean | 1381 | 317 | 4504 | 571 | 573 | 4456 / 564 | 4.1 |
| github | lean-nomouse | 1381 | 209 | 4487 | 568 | 570 | 4456 / 564 | 2.2 |

Key observations (measured):

- **The full snapshot dominates.** It is 89–98% of a 90 s recording. Incremental activity is only
  **4–11 KB/min gzipped**, even with continuous scripted activity.
- **Inlined CSS drives snapshot size.** CSS text is 89% of GitHub's snapshot (4.08 of 4.56 MB raw),
  43% of Wikipedia's and 18% of MDN's.
- **The lean config saves little in total** (~1–3%). It cuts events by 35–60% and incremental bytes by 20–35%;
  mousemove is the biggest incremental component.
- Gzipping 10 s chunks separately costs only ~2% over one stream.

## Part B: upload CPU (measured; Ryzen 9 7950X, Go 1.27, `-cpu 1`)

Handler: `http.MaxBytesReader(1 MiB)` + `io.Copy` into an `O_APPEND` file (page cache, no fsync, no decompression), 64 KiB gzipped body.

| benchmark | ns/op | MB/s | chunks/s (1 core) | B/op | allocs/op |
|---|---:|---:|---:|---:|---:|
| handler only (httptest recorder) | 39,000–42,000 | 1,560–1,680 | ~24,000–25,600 | 38,609 | 19 |
| real loopback HTTP server (client on the same core) | 90,000–94,500 | 690–725 | ~10,600–11,100 (lower bound) | 71,762 | 76 |

The 38 KB/op is io.Copy's 32 KiB buffer: MaxBytesReader hides WriterTo, so the fast copy path is skipped.
A pooled `io.CopyBuffer` would remove it. Not worth it yet.

## Estimates (extrapolated, not measured)

Session-size model: **one full snapshot per page load** (rrweb re-snapshots on every navigation, but not on SPA
route changes) **plus ~5–10 KB per minute**. Assumed typical recorded session: 2.5 page loads, 3 minutes.

| site type (like) | per recorded session-minute, steady state | typical session (est.) | sessions per 5 GiB (est.) |
|---|---|---|---|
| docs (MDN) | ~9 KB + 80 KB per page load | ~225 KB | ~23,000 |
| content (Wikipedia) | ~7 KB + 115 KB per page load | ~310 KB | ~17,000 |
| CSS-heavy app (GitHub) | ~4 KB + 565 KB per page load | ~1.4 MB | ~3,700 |

At **15% sampling with 30-day retention**, 5 GiB holds about **115k–150k total sessions/month** for docs or
content sites (est.). For a CSS-heavy site it holds only **~25k/month**. Upload CPU is negligible: a ~300 KB
session is ~5 chunks, ~0.5 ms of one core (est.), so one core can absorb ~10k chunks/s.

## Recommendation

- **The defaults are sane** for small and medium sites (up to ~100k sessions/month). Keep 15%, 5 GB, 30 days.
- **Budget on page loads, not minutes.** Rough rule: about 100–600 KB per page load, plus ~10 KB/min.
- **When the quota is full, stop recording; don't evict.** Show "quota reached" in the UI.
- **The biggest lever is the snapshot, not sampling knobs.** Dedupe inlined stylesheets server-side by
  content hash (per site), or have the recorder reference same-origin CSS URLs. This would cut
  CSS-heavy sites by ~5–10× (est.).
- Use the lean config anyway: it gives fewer events and less client CPU. `mousemove:false` is reasonable
  if heatmaps don't need replay data.
