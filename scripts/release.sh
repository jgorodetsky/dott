#!/usr/bin/env bash
# release.sh VERSION - tag a release, publish it on GitHub, and point the Homebrew formula at it.
#   scripts/release.sh 0.1.0
# needs: a clean main that matches origin, gh logged in, and a checkout of the tap next to this
# repo (../homebrew-dott) or at $DOTT_TAP. it runs scripts/check.sh first. signing prompts may pop
# up for the tag and the formula commit.
set -euo pipefail
cd "$(dirname "$0")/.."

v="${1:?usage: scripts/release.sh VERSION (like 0.1.0)}"
v="${v#v}"
[[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "release.sh: $v is not a version like 0.1.0" >&2; exit 2; }
tag="v$v"
tap="${DOTT_TAP:-$(cd .. && pwd)/homebrew-dott}"
formula="$tap/Formula/dott.rb"
[ -f "$formula" ] || { echo "release.sh: no formula at $formula (clone jgorodetsky/homebrew-dott, or set DOTT_TAP)" >&2; exit 2; }

[ "$(git branch --show-current)" = main ] || { echo "release.sh: not on main" >&2; exit 1; }
[ -z "$(git status --porcelain)" ] || { echo "release.sh: the tree has changes" >&2; exit 1; }
git fetch --quiet origin
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || { echo "release.sh: main differs from origin/main" >&2; exit 1; }
! git rev-parse -q --verify "refs/tags/$tag" >/dev/null || { echo "release.sh: $tag exists" >&2; exit 1; }

scripts/check.sh

sign=-a
[ -n "$(git config user.signingkey || true)" ] && sign=-s
git tag "$sign" "$tag" -m "dott $v"
git push --quiet origin "$tag"
gh release create "$tag" --verify-tag --title "dott $v" --generate-notes

url="https://github.com/jgorodetsky/dott/archive/refs/tags/$tag.tar.gz"
sum="$(curl -fsSL "$url" | shasum -a 256 | cut -d' ' -f1)"
[ "${#sum}" = 64 ] || { echo "release.sh: could not hash $url" >&2; exit 1; }

git -C "$tap" pull --quiet --ff-only
sed -i '' -E "s|^  url \".*\"|  url \"$url\"|; s|^  sha256 \".*\"|  sha256 \"$sum\"|" "$formula"
grep -q "$sum" "$formula" || { echo "release.sh: the formula didn't take the new sha256" >&2; exit 1; }
git -C "$tap" commit --quiet -m "dott $v" -- Formula/dott.rb
git -C "$tap" push --quiet
echo "released $tag: $url"
echo "sha256 $sum"
echo "next: brew update && brew upgrade jgorodetsky/dott/dott"
