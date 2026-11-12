#!/usr/bin/env bash
# Exercise release operations in a disposable repository.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd -P)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE MAKEFLAGS MFLAGS MAKELEVEL
mkdir -p "$tmp/repo/scripts"
cp "$root/Makefile" "$tmp/repo/Makefile"
cp "$root/scripts/release.py" "$tmp/repo/scripts/release.py"
cd "$tmp/repo"
git init -q --initial-branch=main
git config user.name 'Release Test'
git config user.email 'release@example.invalid'
git config tag.gpgSign false
git add .
git commit -qm seed
original=$(git rev-parse HEAD)
make release VERSION=patch SKIP_TESTS=1 DRY_RUN=1 AUTOPUSH=1
test -z "$(git tag)"
make release VERSION=patch SKIP_TESTS=1
test "$(git cat-file -t v0.0.1)" = tag
test "$(git rev-parse 'v0.0.1^{}')" = "$original"
test "$(make -s release-list)" = v0.0.1
for version in 0.0.1 01.0.0 1.2; do
    if make release VERSION="$version" SKIP_TESTS=1 >"$tmp/output" 2>&1; then
        echo "accepted invalid or existing version $version" >&2; exit 1
    fi
done
git switch -qc other
if make release VERSION=minor SKIP_TESTS=1 >"$tmp/output" 2>&1; then
    echo 'accepted non-main branch' >&2; exit 1
fi
git switch -q main
echo 'user work' > dirty.txt
if make release VERSION=minor SKIP_TESTS=1 >"$tmp/output" 2>&1; then
    echo 'accepted dirty tree' >&2; exit 1
fi
test "$(cat dirty.txt)" = 'user work'
test -z "$(git tag -l v0.1.0)"
echo 'release fixtures passed'
