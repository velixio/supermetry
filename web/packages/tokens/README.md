# Shared design tokens

`tokens.json` is the source for generated dashboard compatibility CSS, site aliases, and chart values. Run `bun run web/packages/tokens/build.ts` from `web/` to rebuild `dist/`.

## Dashboard swap (after the parallel brand work merges)

1. Keep the Tailwind imports and `@theme inline` setup in `web/src/index.css`.
2. Import `../packages/tokens/dist/tokens.css` and `../packages/tokens/dist/shadcn.css` after the framework imports.
3. Remove the hand-authored theme variable blocks only after comparing the light and `.dark` output; generated `--background`, `--primary`, sidebar and chart aliases preserve their current values.
4. Set `data-density` on the root element to select compact or comfortable sizing.

## Marketing site sync

Copy `dist/tokens.css` and `dist/site.css` into the site styles token directory as part of its pinned-core-release sync. Import them after `global.css`'s framework/font imports and remove duplicated aliases when the site sync lands. Today's site differs from the dashboard: IBM Plex Sans/Mono instead of system stacks; white/neutral-gray surfaces and neutral text; `#4f39f6` / `#7c74ff` violet rather than OKLCH indigo; separate hover, tint, aurora, danger/success colors; 4/6/10px radii and a larger spacing scale. This first package deliberately preserves the dashboard's palette for the later dashboard swap; adopting it on the site will make those shared values converge.

## Checks

`bun run web/packages/tokens/build.ts`, `bun run test` (Vitest), and `bun run web/packages/tokens/scripts/usage-lint.ts`. The usage lint is report-only until component cleanup is scheduled.

Contrast review: the current dashboard muted foreground (`oklch(0.5 0.03 285)`) is below 4.5:1 against the light page and raised surfaces. It is preserved verbatim for this migration-parity phase; updating it requires a separately approved visual change in the dashboard theme source. The status is intentionally called out rather than silently changing the current appearance.
