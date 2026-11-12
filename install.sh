#!/usr/bin/env bash
# Installs the heyrobots server, heyrobotsd, for this Mac or Linux machine:
#
#   curl -fsSL https://raw.githubusercontent.com/sdsvn/heyrobots/main/install.sh | bash
#
# It picks the latest release for this system, checks it against the
# release's checksums, and puts heyrobotsd in ~/.local/bin.
#
# While the repository is private, anonymous downloads fail. Then it uses
# the GitHub CLI if it is signed in (gh auth login), or a token in
# GITHUB_TOKEN with read access to the repository.
#
# Settings, all optional:
#   HEYROBOTS_VERSION      a release tag such as v0.4.0 (default: the latest)
#   HEYROBOTS_INSTALL_DIR  where heyrobotsd goes (default: ~/.local/bin)
#   HEYROBOTS_REPO         owner/name of the repository (default: sdsvn/heyrobots)
#   HEYROBOTS_BASE_URL     download from this folder instead of GitHub, for a
#                          mirror; it must hold the archive and SHA256SUMS.txt
set -euo pipefail

repo="${HEYROBOTS_REPO:-sdsvn/heyrobots}"
version="${HEYROBOTS_VERSION:-latest}"
dest="${HEYROBOTS_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
fail() { printf 'heyrobots install: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "heyrobotsd runs on macOS and Linux, not $(uname -s)." ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "no build for $(uname -m); build from source instead (see the README)." ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
  sha() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "needs sha256sum or shasum to check the download."
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

api="https://api.github.com/repos/$repo"
auth=()
[ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $GITHUB_TOKEN")

tag() {
  if [ "$version" != latest ]; then printf '%s' "$version"; return; fi
  local t
  t="$(curl -fsSL ${auth[@]+"${auth[@]}"} "$api/releases/latest" 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)" || true
  if [ -z "$t" ] && command -v gh >/dev/null 2>&1; then
    t="$(gh release view --repo "$repo" --json tagName --jq .tagName 2>/dev/null)" || true
  fi
  [ -n "$t" ] || fail "couldn't find a release of $repo. If it's private, sign in with 'gh auth login' or set GITHUB_TOKEN."
  printf '%s' "$t"
}

if [ -n "${HEYROBOTS_BASE_URL:-}" ]; then
  [ "$version" != latest ] || fail "set HEYROBOTS_VERSION with HEYROBOTS_BASE_URL."
  t="$version"
  name="heyrobotsd-$t-$os-$arch"
  curl -fsSL -o "$work/$name.tar.gz" "$HEYROBOTS_BASE_URL/$name.tar.gz" || fail "couldn't download $name.tar.gz from $HEYROBOTS_BASE_URL."
  curl -fsSL -o "$work/SHA256SUMS.txt" "$HEYROBOTS_BASE_URL/SHA256SUMS.txt" || fail "couldn't download SHA256SUMS.txt."
else
  t="$(tag)"
  name="heyrobotsd-$t-$os-$arch"
  say "Downloading heyrobotsd $t for $os/$arch…"
  if curl -fsSL -o "$work/$name.tar.gz" "https://github.com/$repo/releases/download/$t/$name.tar.gz" 2>/dev/null &&
    curl -fsSL -o "$work/SHA256SUMS.txt" "https://github.com/$repo/releases/download/$t/SHA256SUMS.txt" 2>/dev/null; then
    :
  elif command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    gh release download "$t" --repo "$repo" --dir "$work" --clobber --pattern "$name.tar.gz" --pattern SHA256SUMS.txt ||
      fail "couldn't download $name.tar.gz with the GitHub CLI."
  elif [ -n "${GITHUB_TOKEN:-}" ]; then
    # A private repository's files come through the API, by asset id.
    assets="$(curl -fsSL ${auth[@]+"${auth[@]}"} "$api/releases/tags/$t")" || fail "couldn't read release $t; check GITHUB_TOKEN."
    # One line per JSON object: an asset's id and name share its line.
    objects="$(printf '%s' "$assets" | tr -d '\n' | tr '{' '\n')"
    for want in "$name.tar.gz" SHA256SUMS.txt; do
      id="$(printf '%s\n' "$objects" | grep "\"name\": *\"$want\"" | sed -n 's/.*"id": *\([0-9]*\).*/\1/p' | head -n1)"
      [ -n "$id" ] || fail "release $t has no $want."
      curl -fsSL ${auth[@]+"${auth[@]}"} -H "Accept: application/octet-stream" -o "$work/$want" "$api/releases/assets/$id" || fail "couldn't download $want."
    done
  else
    fail "couldn't download $name.tar.gz. If $repo is private, sign in with 'gh auth login' or set GITHUB_TOKEN, then run this again."
  fi
fi

want="$(grep " $name.tar.gz\$" "$work/SHA256SUMS.txt" | cut -d' ' -f1 | head -n1)"
[ -n "$want" ] || fail "SHA256SUMS.txt doesn't list $name.tar.gz."
[ "$(sha "$work/$name.tar.gz")" = "$want" ] || fail "the download doesn't match its checksum; nothing was installed."

tar -xzf "$work/$name.tar.gz" -C "$work"
mkdir -p "$dest"
install -m 0755 "$work/$name/heyrobotsd" "$dest/heyrobotsd"
# Not signed by Apple: clear the "downloaded from the internet" mark, or
# macOS refuses to start it.
[ "$os" = darwin ] && xattr -c "$dest/heyrobotsd" 2>/dev/null || true

say "Installed heyrobotsd $t in $dest."
case ":$PATH:" in
  *":$dest:"*) run=heyrobotsd ;;
  *)
    run="$dest/heyrobotsd"
    say "$dest isn't on your PATH. Add it with:"
    say "  echo 'export PATH=\"$dest:\$PATH\"' >> ~/.$(basename "${SHELL:-bash}")rc"
    ;;
esac
say ""
say "Start it with:"
say "  $run"
say "It prints a setup code; open http://127.0.0.1:7340/setup on this machine and enter it."
say "To reach it from your phone, see https://github.com/$repo#reaching-it-from-your-phone"
