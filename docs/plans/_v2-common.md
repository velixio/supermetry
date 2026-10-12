## Common rules (all v2 jobs)
- You work in a git worktree of the core repo (Go + PostgreSQL analytics, React/TanStack dashboard in web/). Stay on its branch. Product plan: /home/pal/github/supermetry-mono/supermetry/docs/plans/2026-10-12-v2-app-shell-realtime.md (read it).
- Hard rules: Go + PostgreSQL only (Redis optional), no DB read on the ingest hot path, privacy first, never drop data silently.
- /tmp has a quota: prefix every go/bun/make command with `TMPDIR=$HOME/.cache/go-tmp GOTMPDIR=$HOME/.cache/go-tmp`.
- Web deps: `cd web && bun install --frozen-lockfile`. Go tests that need Postgres: use the Makefile env (`make -n test-fast` shows how it starts the dev DB on :5433); if Docker is unavailable in your sandbox, run the non-DB tests and say so.
- Never touch /home/pal/github/supermetry-mono/supermetry (the main checkout; another session has uncommitted work there) except to READ files when this spec says so. Do not stop or start servers on :8787/:8788.
- Commit when green with identity `git -c user.name=velixio -c user.email=270455167+velixio@users.noreply.github.com commit`; conventional messages like the history (`fix(writer): …`); no trailers, no mention of AI/Codex/Claude. Scan `git diff --cached` for email addresses first (only the velixio noreply allowed). If git refuses to write (sandbox), leave changes uncommitted and say so. Do not push.
- Every behaviour change gets a test. Report (≤300 words): commits, what changed, test commands + pass/fail counts, what you skipped and why.
