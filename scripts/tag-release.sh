#!/usr/bin/env bash
# Tag and push a Supermetry release; the tag starts .github/workflows/release.yml.
#
#   make release                     ask for the version, default is the next patch
#   make release BUMP=minor          next minor (0.1.0 -> 0.2.0) without asking
#   make release VERSION=0.2.0       an exact version without asking
#   make release DRY_RUN=1           show what would happen, change nothing
#
# Flags (what make passes): --version X.Y.Z, --bump patch|minor|major, --yes
# (skip the final confirmation, required when there is no terminal), --dry-run.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="" bump="" assume_yes=0 dry_run=0
while (($#)); do
    case "$1" in
        --version) version="${2:-}"; shift 2 ;;
        --bump) bump="${2:-}"; shift 2 ;;
        --yes) assume_yes=1; shift ;;
        --dry-run) dry_run=1; shift ;;
        -h | --help) sed -n '2,11p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "tag-release: unknown argument: $1" >&2; exit 2 ;;
    esac
done

die() { echo "tag-release: $*" >&2; exit 1; }

# Prompts read stdin; they run only on a terminal (RELEASE_FORCE_PROMPT is for tests).
interactive=0
if [[ -t 0 || -n "${RELEASE_FORCE_PROMPT:-}" ]]; then interactive=1; fi
ask() { # ask "question" default -> answer in $REPLY
    local answer=""
    read -r -p "$1" answer || true
    REPLY="${answer:-$2}"
}

command -v git >/dev/null 2>&1 || die "git is required"
use_gh=0
if [[ -z "${RELEASE_SKIP_GH:-}" ]] && command -v gh >/dev/null 2>&1; then use_gh=1; fi

# --- Preconditions ---------------------------------------------------------
email="$(git config user.email || true)"
[[ "$email" == *@users.noreply.github.com ]] ||
    die "git user.email is '$email'; use the project noreply identity so no private address lands in the tag"

branch="$(git rev-parse --abbrev-ref HEAD)"
[[ "$branch" == "main" ]] || die "releases are cut from main; you are on '$branch'"
[[ -z "$(git status --porcelain --untracked-files=no)" ]] || die "the working tree has uncommitted changes"

git fetch -q origin main --tags || die "could not fetch from origin"
local_sha="$(git rev-parse HEAD)"
remote_sha="$(git rev-parse origin/main)"
[[ "$local_sha" == "$remote_sha" ]] ||
    die "local main (${local_sha:0:8}) differs from origin/main (${remote_sha:0:8}); pull or push first"

# --- Versions --------------------------------------------------------------
semver_re='^[0-9]+\.[0-9]+\.[0-9]+$'
latest_tag="$(git tag --list 'v[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n1 || true)"
latest="${latest_tag#v}"
latest="${latest:-0.0.0}"
IFS=. read -r major minor patch <<<"$latest"
next_patch="$major.$minor.$((patch + 1))"
next_minor="$major.$((minor + 1)).0"
next_major="$((major + 1)).0.0"

bump_to_version() {
    case "$1" in
        patch) echo "$next_patch" ;;
        minor) echo "$next_minor" ;;
        major) echo "$next_major" ;;
        *) return 1 ;;
    esac
}

echo "Latest release: ${latest_tag:-none}"
echo "Next version:   $next_patch (patch, default)   $next_minor (minor)   $next_major (major)"

if [[ -n "$version" && -n "$bump" ]]; then die "use either --version or --bump, not both"; fi
if [[ -n "$bump" ]]; then
    version="$(bump_to_version "$bump")" || die "--bump must be patch, minor or major"
elif [[ -z "$version" ]]; then
    if ((interactive)); then
        ask "Version to release [$next_patch]: " "$next_patch"
        version="$REPLY"
        if mapped="$(bump_to_version "$version")"; then version="$mapped"; fi
    else
        version="$next_patch"
        echo "No version given and no terminal: using the default $version."
    fi
fi
version="${version#v}"

[[ "$version" =~ $semver_re ]] || die "'$version' is not a plain X.Y.Z version (pre-releases are not published)"
tag="v$version"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null || git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
    die "tag $tag already exists; releases are never re-tagged, pick the next version"
fi
if [[ -n "$latest_tag" && "$(printf '%s\n%s\n' "$latest" "$version" | sort -V | tail -n1)" != "$version" ]]; then
    die "$version is not newer than the latest release $latest"
fi

# --- Warnings --------------------------------------------------------------
warnings=()
if ! grep -qE "^## v$version( |$)" CHANGELOG.md 2>/dev/null; then
    warnings+=("CHANGELOG.md has no '## v$version' section; move the Unreleased notes under it first")
fi
if ((use_gh)); then
    ci="$(gh run list --commit "$local_sha" --json workflowName,status,conclusion \
        --jq '[.[] | select(.conclusion == "failure" or .conclusion == "cancelled")] | length' 2>/dev/null || true)"
    total="$(gh run list --commit "$local_sha" --json databaseId --jq 'length' 2>/dev/null || true)"
    if [[ "${ci:-0}" != "0" ]]; then
        warnings+=("$ci workflow run(s) failed or were cancelled for this commit")
    elif [[ "${total:-0}" == "0" ]]; then
        warnings+=("no workflow runs found for this commit, so CI status is unknown")
    fi
else
    warnings+=("gh is not available, so CI status was not checked")
fi

echo
echo "Release:  $tag"
echo "Commit:   $(git log -1 --format='%h %s' "$local_sha")"
for warning in "${warnings[@]}"; do echo "Warning:  $warning"; done
echo

if ((dry_run)); then
    echo "Dry run: would run 'git tag -a $tag' and 'git push origin $tag'. Nothing was changed."
    exit 0
fi

if ((!assume_yes)); then
    ((interactive)) || die "no terminal to confirm on; pass YES=1 (--yes) to release without asking"
    ask "Create and push tag $tag? This starts the public release build. [y/N]: " "n"
    [[ "$REPLY" == [yY] || "$REPLY" == [yY][eE][sS] ]] || { echo "Cancelled."; exit 0; }
fi

git tag -a "$tag" -m "$tag"
git push origin "$tag"
echo
echo "Pushed $tag. The release workflow is starting."
if ((use_gh)); then
    echo "Watch it:  gh run watch --exit-status \$(gh run list --workflow release.yml --limit 1 --json databaseId -q '.[0].databaseId')"
    echo "Runs:      $(gh repo view --json url -q .url 2>/dev/null || echo "see the Actions tab")/actions/workflows/release.yml"
fi
