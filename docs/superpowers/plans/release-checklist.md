# Release checklist for v0.1.0 (maintainer steps)

Nothing here has been run for you. The repository is private, nothing is tagged, and CI runs only when started by hand. Do the steps in order and tick each one.

Placeholders: `OWNER/REPO` is `velixio/supermetry`. Never put a password, token or private email address into a commit, an issue or a pull request.

## 1. Check the tree

| Step | Command | Expected |
|---|---|---|
| Clean state on main | `git switch main && git pull --ff-only && git status -sb` | `## main...origin/main`, nothing modified |
| Go formatting and vet | `gofmt -l cmd internal && go vet ./...` | no output |
| Go tests (about 15 minutes) | `SUPERMETRY_TEST_DSN='postgres://USER:PASS@127.0.0.1:5433/supermetry_test?sslmode=disable' go test -count=1 -p 3 -timeout 25m ./...` | every package `ok` |
| Web tests and build | `cd web && bun install --frozen-lockfile && bun run typecheck && bun run test --run && bun run build` | all pass; the build prints `Initial JavaScript: ... / 256000 bytes gzipped` below the budget |
| End-to-end specs | `make test-e2e` (or the groups in the Makefile, at most two login-form specs per command) | all pass; `privacy-navigation` and `navigation` can fail when many login specs run back to back (login rate limit): rerun them alone |
| Migration numbers are unique | `ls internal/db/migrations \| cut -d_ -f1 \| sort \| uniq -d` | no output |

## 2. Clean private data

1. Rotate every password and token that was shared while developing: the demo instance admin, the reference products used for comparison (Plausible, Umami, OpenPanel), database users and any API keys. Do this in each system, not in git.
2. Scan the whole history for private addresses and secrets before the repository becomes public:
   - `git log --all --format='%an <%ae>%n%cn <%ce>' | sort -u` should list only `velixio <270455167+velixio@users.noreply.github.com>`.
   - `git grep -nIE '[[:alnum:]._+-]+@[[:alnum:].-]+' $(git rev-list --all) -- . ':!knowledge' | grep -vE 'noreply|example\.|\.test' | head` should show nothing private.
   - `git log --all -p | grep -nE 'BEGIN [A-Z ]*PRIVATE KEY|ghp_|xox[bap]-|AKIA[0-9A-Z]{16}' | head` should show nothing.
3. Earlier planning files named the tool used to run jobs. Removing that from history needs a history rewrite and a force-push. **Do not do this without deciding it yourself**; if you decide to, rewrite on a fresh clone, and tell collaborators to re-clone.

## 3. Align the docs site

1. Review and merge the documentation pull request: https://github.com/velixio/supermetry-site/pull/1 (`gh pr view 1 --repo velixio/supermetry-site`, then `gh pr merge 1 --repo velixio/supermetry-site --squash`).
2. Update its localization page: Spanish and German are now selectable in the language picker (machine-assisted, not native-reviewed).
3. Check that `tracking/install.md` shows the same snippet as the app's install recipes.
4. Build the site (`bun install --frozen-lockfile && bun run build`) and publish it the way you normally do.

## 4. Make the repository public

```sh
gh repo edit velixio/supermetry --visibility public --accept-visibility-change-consequences
gh repo view velixio/supermetry --json visibility -q .visibility   # PUBLIC
```

Then enable secret scanning and push protection in the repository's Security settings.

## 5. Turn CI on

`.github/workflows/ci.yml` runs only by hand. To run it on every push and pull request, replace its trigger block

```yaml
on:
  workflow_dispatch:
  # push:
  # pull_request:
```

with

```yaml
on:
  workflow_dispatch:
  push:
    branches: [main]
  pull_request:
```

Commit that change on its own, push, and watch the first run: `gh run list --workflow ci.yml --limit 3`.

## 6. Tag and release

The release workflow (`.github/workflows/release.yml`) runs when a tag starting with `v` is pushed. It builds the dashboard, cross-compiles six targets (linux, darwin and windows, each for amd64 and arm64), writes `SHA256SUMS`, creates a GitHub Release with the archives and both installer scripts (`install.sh`, `install.ps1`), and pushes a multi-arch image to `ghcr.io/velixio/supermetry` tagged with the version and `latest`.

```sh
# 1. Move the changelog entry out of "unreleased" and commit it
$EDITOR CHANGELOG.md   # "## v0.1.0 (unreleased)" -> "## v0.1.0 (YYYY-MM-DD)"
git add CHANGELOG.md && git commit -m "docs: date the v0.1.0 changelog" && git push

# 2. Tag and push (this starts the release workflow)
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0

# 3. Watch it
gh run watch --exit-status $(gh run list --workflow release.yml --limit 1 --json databaseId -q '.[0].databaseId')
```

To rehearse the build without publishing, run `SUPERMETRY_RELEASE_VERSION=0.1.0 scripts/release.sh` locally and inspect `dist/`.

Verify the result:

```sh
gh release view v0.1.0 --repo velixio/supermetry --json assets -q '.assets[].name'   # 6 archives, SHA256SUMS, install.sh, install.ps1
gh release download v0.1.0 --repo velixio/supermetry --pattern SHA256SUMS --pattern '*linux-amd64*'
sha256sum --check --ignore-missing SHA256SUMS                                          # OK
docker pull ghcr.io/velixio/supermetry:0.1.0 && docker run --rm ghcr.io/velixio/supermetry:0.1.0 version
```

Make the container package public if it is not: GitHub, your profile, Packages, `supermetry`, Package settings, Change visibility.

## 7. Redeploy the demo instance

The demo at `ana.voicestudio.sh` runs an old build. On that server, as an administrator:

1. Back up first: `supermetry backup --help` shows the options; take a dump and keep it somewhere safe.
2. Upgrade with the installer, which keeps your `.env` and data: `curl -fsSL https://github.com/velixio/supermetry/releases/latest/download/install.sh | sh` (add `--dry-run` first to read the plan).
3. Confirm the service is healthy: `systemctl status supermetry` (binary install) or `docker compose ps` (Docker install), then open `/readyz`.
4. Sign in and check the new pages: Sites overview, Revenue, Cohorts, Links and pixels, Integrations, Admin, and the language picker.

## 8. After the release

- Install on a clean machine with the one-line command from the README, create the admin account at `/app`, add a site, paste an install recipe into a test page and press "Verify installation". Expect it to turn green after one pageview.
- Run the installer with `--dry-run` on each OS you support and confirm it changes nothing.
- Open an issue for each item under "Known limits" in the changelog so they are tracked: native review of Spanish and German, remaining untranslated interface text, live Google Search Console testing, and short-link click limits across several instances.
