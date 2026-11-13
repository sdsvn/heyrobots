#!/usr/bin/env bash
# Installs heyrobots on this Mac or Linux machine, ready to use:
#
#   curl -fsSL https://raw.githubusercontent.com/sdsvn/heyrobots/main/install.sh | bash
#
# 1. The server, heyrobotsd, in ~/.local/bin, checked against the release's
#    checksums.
# 2. A background service that starts it at login (launchd on a Mac,
#    systemd on Linux).
# 3. On a Mac, the heyrobots app in /Applications (or ~/Applications).
# 4. The app opens already signed in to the server; on Linux, the web app
#    opens in your browser, signed in.
#
# Running it again upgrades everything in place. While the repository is
# private, anonymous downloads fail; then it uses the GitHub CLI if it is
# signed in (gh auth login), or a token in GITHUB_TOKEN.
#
# Settings, all optional:
#   HEYROBOTS_VERSION      a release tag such as v0.2.0 (default: the latest)
#   HEYROBOTS_INSTALL_DIR  where heyrobotsd goes (default: ~/.local/bin)
#   HEYROBOTS_NO_SERVICE=1 install the server only; don't start it at login
#   HEYROBOTS_NO_APP=1     don't install or open the app
#   HEYROBOTS_APP_DIR      where the Mac app goes (default: /Applications,
#                          or ~/Applications when that isn't writable)
#   HEYROBOTS_REPO         owner/name of the repository (default: sdsvn/heyrobots)
#   HEYROBOTS_BASE_URL     download from this folder instead of GitHub
# Other HEYROBOTS_* settings set when you run it, such as HEYROBOTS_LISTEN or
# HEYROBOTS_ACP_COMMAND, are passed on to the service.
set -euo pipefail

repo="${HEYROBOTS_REPO:-sdsvn/heyrobots}"
version="${HEYROBOTS_VERSION:-latest}"
dest="${HEYROBOTS_INSTALL_DIR:-${HOME}/.local/bin}"
label="dev.heyrobots.heyrobotsd"

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
trap 'rm -rf "${work}"' EXIT

api="https://api.github.com/repos/${repo}"
auth=()
[ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
have_gh() { command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; }

# The release to install.
if [ "${version}" != latest ]; then
  t="${version}"
elif [ -n "${HEYROBOTS_BASE_URL:-}" ]; then
  fail "set HEYROBOTS_VERSION with HEYROBOTS_BASE_URL."
else
  t="$(curl -fsSL ${auth[@]+"${auth[@]}"} "${api}/releases/latest" 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)" || true
  if [ -z "${t}" ] && have_gh; then
    t="$(gh release view --repo "${repo}" --json tagName --jq .tagName 2>/dev/null)" || true
  fi
  [ -n "${t}" ] || fail "couldn't find a release of ${repo}. If it's private, sign in with 'gh auth login' or set GITHUB_TOKEN."
fi

# fetch FILE: puts the release file FILE in ${work}, by the first way that works.
assets=""
fetch() {
  local f="$1" id
  if [ -n "${HEYROBOTS_BASE_URL:-}" ]; then
    curl -fsSL -o "${work}/${f}" "${HEYROBOTS_BASE_URL}/${f}" && return 0
    return 1
  fi
  curl -fsSL -o "${work}/${f}" "https://github.com/${repo}/releases/download/${t}/${f}" 2>/dev/null && return 0
  if have_gh; then
    gh release download "${t}" --repo "${repo}" --dir "${work}" --clobber --pattern "${f}" >/dev/null 2>&1 && return 0
  fi
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    # A private repository's files come through the API, by asset id. One
    # line per JSON object: an asset's id and name share its line.
    [ -n "${assets}" ] || assets="$(curl -fsSL ${auth[@]+"${auth[@]}"} "${api}/releases/tags/${t}" | tr -d '\n' | tr '{' '\n')" || return 1
    id="$(printf '%s\n' "${assets}" | grep "\"name\": *\"${f}\"" | sed -n 's/.*"id": *\([0-9]*\).*/\1/p' | head -n1)"
    [ -n "${id}" ] || return 1
    curl -fsSL ${auth[@]+"${auth[@]}"} -H "Accept: application/octet-stream" -o "${work}/${f}" "${api}/releases/assets/${id}" && return 0
  fi
  return 1
}

# verify FILE: fails unless FILE matches the release's checksum for it.
verify() {
  local want
  want="$(grep " ${1}\$" "${work}/SHA256SUMS.txt" | cut -d' ' -f1 | head -n1)"
  [ -n "${want}" ] || fail "SHA256SUMS.txt doesn't list ${1}."
  [ "$(sha "${work}/${1}")" = "${want}" ] || fail "${1} doesn't match its checksum; nothing was installed."
}

say "Installing heyrobots ${t} for ${os}/${arch}..."
fetch SHA256SUMS.txt || fail "couldn't download release ${t} of ${repo}. If it's private, sign in with 'gh auth login' or set GITHUB_TOKEN, then run this again."

# 1. The server.
name="heyrobotsd-${t}-${os}-${arch}"
fetch "${name}.tar.gz" || fail "couldn't download ${name}.tar.gz."
verify "${name}.tar.gz"
tar -xzf "${work}/${name}.tar.gz" -C "${work}"
mkdir -p "${dest}"
install -m 0755 "${work}/${name}/heyrobotsd" "${dest}/heyrobotsd"
# Not signed by Apple: clear the "downloaded from the internet" mark, or
# macOS refuses to start it.
if [ "${os}" = darwin ]; then xattr -c "${dest}/heyrobotsd" 2>/dev/null || true; fi
say "  Server:  ${dest}/heyrobotsd"

# The settings the service runs with: PATH, so it finds claude, omp, npx
# and docker, and any HEYROBOTS_* setting given to this script.
envs=("PATH=${PATH}")
while IFS='=' read -r k v; do
  case "${k}" in
    HEYROBOTS_VERSION | HEYROBOTS_INSTALL_DIR | HEYROBOTS_NO_SERVICE | HEYROBOTS_NO_APP | HEYROBOTS_APP_DIR | HEYROBOTS_REPO | HEYROBOTS_BASE_URL) ;;
    HEYROBOTS_*) envs+=("${k}=${v}") ;;
  esac
done < <(env)

listen="${HEYROBOTS_LISTEN:-127.0.0.1:7340}"
listen="${listen%%,*}"
case "${listen}" in 0.0.0.0:* | \[::\]:*) listen="127.0.0.1:${listen##*:}" ;; esac
local_url="http://${listen}"
[ -n "${HEYROBOTS_TLS_CERT:-}" ] && local_url="https://${listen}"

xml() { printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

# 2. The service.
service=no
answers() { curl -fsS -o /dev/null --max-time 2 "${local_url}/api/v1/health" 2>/dev/null; }
if [ -z "${HEYROBOTS_NO_SERVICE:-}" ]; then
  # Stop an earlier copy of this service, then make sure nothing else, such
  # as a server started by hand, already holds the address.
  if [ "${os}" = darwin ]; then
    launchctl bootout "gui/$(id -u)/${label}" >/dev/null 2>&1 || true
  elif command -v systemctl >/dev/null 2>&1; then
    systemctl --user stop heyrobots >/dev/null 2>&1 || true
  fi
  sleep 1
  # A server started by hand holds the data folder, whatever address it
  # listens on; a second one would share its database.
  if "${dest}/heyrobotsd" status >/dev/null 2>&1 || answers; then
    fail "a heyrobots server is already running on this machine. Stop it (Ctrl+C where it runs), then run this again. Or set HEYROBOTS_NO_SERVICE=1 to keep running it yourself."
  fi
  if [ "${os}" = darwin ]; then
    plist="${HOME}/Library/LaunchAgents/${label}.plist"
    logs="${HOME}/Library/Logs/heyrobots.log"
    mkdir -p "${HOME}/Library/LaunchAgents" "${HOME}/Library/Logs"
    {
      printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' \
        '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
        '<plist version="1.0"><dict>' \
        "<key>Label</key><string>${label}</string>" \
        "<key>ProgramArguments</key><array><string>$(xml "${dest}/heyrobotsd")</string></array>" \
        '<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>' \
        "<key>StandardOutPath</key><string>$(xml "${logs}")</string>" \
        "<key>StandardErrorPath</key><string>$(xml "${logs}")</string>" \
        '<key>EnvironmentVariables</key><dict>'
      for e in "${envs[@]}"; do
        printf '<key>%s</key><string>%s</string>\n' "$(xml "${e%%=*}")" "$(xml "${e#*=}")"
      done
      printf '%s\n' '</dict></dict></plist>'
    } >"${plist}"
    launchctl bootstrap "gui/$(id -u)" "${plist}" || fail "couldn't start the service; see ${plist}."
    say "  Service: starts at login (launchd); log in ${logs}"
    service=yes
  elif command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
    unit="${HOME}/.config/systemd/user/heyrobots.service"
    mkdir -p "$(dirname "${unit}")"
    {
      printf '%s\n' '[Unit]' 'Description=heyrobots' '' '[Service]' "ExecStart=${dest}/heyrobotsd" 'Restart=on-failure'
      for e in "${envs[@]}"; do
        printf 'Environment="%s"\n' "$(printf '%s' "${e}" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/%/%%/g')"
      done
      printf '%s\n' '' '[Install]' 'WantedBy=default.target'
    } >"${unit}"
    systemctl --user daemon-reload
    systemctl --user enable heyrobots >/dev/null 2>&1
    systemctl --user restart heyrobots || fail "couldn't start the service; see 'journalctl --user -u heyrobots'."
    say "  Service: starts at login (systemd); log with 'journalctl --user -u heyrobots'"
    say "           To keep it running after you log out: loginctl enable-linger ${USER}"
    service=yes
  else
    say "  Service: not set up (no systemd user session); start it with ${dest}/heyrobotsd"
  fi
fi

# 3. The app.
app=""
if [ -z "${HEYROBOTS_NO_APP:-}" ] && [ "${os}" = darwin ]; then
  zip="heyrobots-app-${t}-macos.zip"
  if fetch "${zip}"; then
    verify "${zip}"
    apps="${HEYROBOTS_APP_DIR:-/Applications}"
    [ -n "${HEYROBOTS_APP_DIR:-}" ] || [ -w "${apps}" ] || apps="${HOME}/Applications"
    mkdir -p "${apps}"
    rm -rf "${apps}/heyrobots.app"
    ditto -x -k "${work}/${zip}" "${apps}"
    # Installed from here, not a browser download, so macOS opens it; clear
    # the mark anyway in case something added it.
    xattr -cr "${apps}/heyrobots.app" 2>/dev/null || true
    app="${apps}/heyrobots.app"
    say "  App:     ${app}"
  else
    say "  App:     not in release ${t}; use the web app at ${local_url}"
  fi
fi

# 4. Open it, signed in.
if [ "${service}" = yes ] && [ -z "${HEYROBOTS_NO_APP:-}" ]; then
  up=no
  for _ in $(seq 1 40); do
    if answers; then up=yes; break; fi
    sleep 0.5
  done
  if [ "${up}" = yes ]; then
    if [ -n "${app}" ]; then
      # Open the link with this copy: macOS might hand heyrobots:// links to
      # another one, such as an old download.
      link="$("${dest}/heyrobotsd" pair --link 2>/dev/null)" || link=""
      say ""
      if [ -n "${link}" ]; then
        open -a "${app}" "${link}"
        say "heyrobots is open and signing in. It runs in the background from now on."
      else
        open -a "${app}"
        say "heyrobots is open. Pair it with the code in ${HOME}/Library/Logs/heyrobots.log."
      fi
    else
      page="$("${dest}/heyrobotsd" pair --web 2>/dev/null)" || page="${local_url}"
      if command -v xdg-open >/dev/null 2>&1; then xdg-open "${page}" >/dev/null 2>&1 || true; elif [ "${os}" = darwin ]; then open "${page}"; fi
      say ""
      say "heyrobots is running. Open ${page} in a browser on this machine to sign in."
      say "The browser forgets the sign-in when you reload; the server prints a new code at each start."
    fi
    exit 0
  fi
  say ""
  say "The service didn't answer at ${local_url} yet."
fi

say ""
case ":${PATH}:" in
  *":${dest}:"*) run=heyrobotsd ;;
  *)
    run="${dest}/heyrobotsd"
    say "${dest} isn't on your PATH. Add it with:"
    say "  echo 'export PATH=\"${dest}:\${PATH}\"' >> ~/.$(basename "${SHELL:-bash}")rc"
    ;;
esac
say "Start it with: ${run}"
say "It prints a QR code and a pairing code; open ${local_url} on this machine and enter the code."
