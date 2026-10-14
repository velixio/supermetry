#!/usr/bin/env bash
# Tests scripts/tag-release.sh against throwaway repositories. Nothing here
# touches the real repository, its remote or GitHub.
set -uo pipefail

script_src="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/tag-release.sh"
root="$(mktemp -d "${TMPDIR:-/tmp}/tag-release-test.XXXXXX")"
trap 'rm -rf "$root"' EXIT
export RELEASE_SKIP_GH=1 GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
fail=0

new_repo() { # new_repo name -> sets $repo; origin has main with tag v0.1.0
    local name="$1"
    git init -q --bare "$root/$name.git"
    git clone -q "$root/$name.git" "$root/$name" 2>/dev/null
    repo="$root/$name"
    (
        cd "$repo"
        git config user.name velixio
        git config user.email 270455167+velixio@users.noreply.github.com
        git checkout -q -b main
        mkdir scripts && cp "$script_src" scripts/tag-release.sh
        printf '# Changelog\n\n## Unreleased\n\n## v0.1.0 (2026-10-14)\n' >CHANGELOG.md
        git add -A && git commit -q -m init
        git tag -a v0.1.0 -m v0.1.0
        git push -q origin main v0.1.0
    )
}
run() { (cd "$repo" && "$@" 2>&1); }
remote_tags() { git -C "$root/$1.git" tag --list | tr '\n' ' '; }
check() { # check "name" condition-exit-status
    if [[ "$2" == 0 ]]; then echo "ok   $1"; else echo "FAIL $1"; fail=1; fi
}
contains() { [[ "$1" == *"$2"* ]]; }

new_repo default
out="$(run scripts/tag-release.sh --yes </dev/null)"
check "no terminal and no version uses the next patch" "$([[ "$(remote_tags default)" == *v0.1.1* ]]; echo $?)"
contains "$out" "Next version:   0.1.1 (patch, default)   0.2.0 (minor)   1.0.0 (major)"; check "shows the next versions" $?

new_repo bump
run scripts/tag-release.sh --bump minor --yes </dev/null >/dev/null
check "--bump minor tags v0.2.0" "$([[ "$(remote_tags bump)" == *v0.2.0* ]]; echo $?)"

new_repo exact
run scripts/tag-release.sh --version v1.0.0 --yes </dev/null >/dev/null
check "--version accepts a v prefix" "$([[ "$(remote_tags exact)" == *v1.0.0* ]]; echo $?)"

new_repo enter
printf '\ny\n' | RELEASE_FORCE_PROMPT=1 bash -c "cd '$repo' && scripts/tag-release.sh" >/dev/null 2>&1
check "Enter accepts the default version, y confirms" "$([[ "$(remote_tags enter)" == *v0.1.1* ]]; echo $?)"

new_repo word
printf 'minor\ny\n' | RELEASE_FORCE_PROMPT=1 bash -c "cd '$repo' && scripts/tag-release.sh" >/dev/null 2>&1
check "typing minor picks the next minor" "$([[ "$(remote_tags word)" == *v0.2.0* ]]; echo $?)"

new_repo decline
printf '\nn\n' | RELEASE_FORCE_PROMPT=1 bash -c "cd '$repo' && scripts/tag-release.sh" >/dev/null 2>&1
check "answering n creates no tag" "$([[ "$(remote_tags decline)" == "v0.1.0 " ]]; echo $?)"

new_repo dry
out="$(run scripts/tag-release.sh --dry-run </dev/null)"
check "--dry-run creates no tag" "$([[ "$(remote_tags dry)" == "v0.1.0 " ]]; echo $?)"
contains "$out" "Nothing was changed"; check "--dry-run says so" $?

new_repo noconfirm
out="$(run scripts/tag-release.sh </dev/null)"; status=$?
check "no terminal and no --yes refuses" "$([[ $status -ne 0 && "$(remote_tags noconfirm)" == "v0.1.0 " ]]; echo $?)"

new_repo exists
out="$(run scripts/tag-release.sh --version 0.1.0 --yes </dev/null)"; status=$?
check "an existing tag is refused" "$([[ $status -ne 0 ]]; echo $?)"; contains "$out" "already exists"; check "explains why" $?

new_repo older
run scripts/tag-release.sh --version 0.0.9 --yes </dev/null >/dev/null; status=$?
check "an older version is refused" "$([[ $status -ne 0 && "$(remote_tags older)" == "v0.1.0 " ]]; echo $?)"

new_repo bad
run scripts/tag-release.sh --version 1.0 --yes </dev/null >/dev/null; s1=$?
run scripts/tag-release.sh --version 1.0.0-rc1 --yes </dev/null >/dev/null; s2=$?
check "non X.Y.Z versions are refused" "$([[ $s1 -ne 0 && $s2 -ne 0 ]]; echo $?)"

new_repo dirty
echo x >>"$repo/CHANGELOG.md"
run scripts/tag-release.sh --yes </dev/null >/dev/null; status=$?
check "uncommitted changes are refused" "$([[ $status -ne 0 && "$(remote_tags dirty)" == "v0.1.0 " ]]; echo $?)"

new_repo branch
git -C "$repo" checkout -q -b topic
run scripts/tag-release.sh --yes </dev/null >/dev/null; status=$?
check "a branch other than main is refused" "$([[ $status -ne 0 ]]; echo $?)"

new_repo behind
git -C "$repo" commit -q --allow-empty -m local-only
run scripts/tag-release.sh --yes </dev/null >/dev/null; status=$?
check "main that differs from origin is refused" "$([[ $status -ne 0 ]]; echo $?)"

new_repo identity
git -C "$repo" config user.email you@example.com
run scripts/tag-release.sh --yes </dev/null >/dev/null; status=$?
check "a non-noreply git identity is refused" "$([[ $status -ne 0 ]]; echo $?)"

new_repo changelog
out="$(run scripts/tag-release.sh --dry-run </dev/null)"
contains "$out" "CHANGELOG.md has no '## v0.1.1' section"; check "warns when the changelog has no section" $?

exit $fail
