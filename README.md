# heyrobots

A personal team of AI agents that runs on your own machines. You chat with your agents from your phone or a browser. They work with your mail, calendar, files and apps, and anything that reaches other people waits for your OK.

heyrobots has two parts:

- **The server, `heyrobotsd`.** One program that holds your team, your chats and your keys. It runs on a Mac, a Linux box or a cloud machine, and it serves the web app too. Several servers can join into one team.
- **The app.** The same app runs in a browser, on macOS, on Linux and on Android. It talks to one server at a time.

## Downloads

Every release on the repository's **Releases** page has these files. `vX.Y.Z` is the version.

| File | What it is |
|---|---|
| `heyrobotsd-vX.Y.Z-darwin-arm64.tar.gz` | Server for Apple silicon Macs |
| `heyrobotsd-vX.Y.Z-darwin-amd64.tar.gz` | Server for Intel Macs |
| `heyrobotsd-vX.Y.Z-linux-amd64.tar.gz` | Server for Linux on x86-64 |
| `heyrobotsd-vX.Y.Z-linux-arm64.tar.gz` | Server for Linux on ARM, such as a Raspberry Pi 4 or 5 |
| `heyrobots-app-vX.Y.Z-macos.zip` | App for macOS |
| `heyrobots-app-vX.Y.Z-linux-x64.tar.gz` | App for Linux on x86-64 |
| `heyrobots-app-vX.Y.Z-android.apk` | App for Android |
| `SHA256SUMS.txt` | Checksums for every file above |

Check a download before you use it:

```bash
shasum -a 256 -c SHA256SUMS.txt --ignore-missing
```

## Install the server

One line installs the latest server for your Mac or Linux machine:

```bash
curl -fsSL https://raw.githubusercontent.com/sdsvn/heyrobots/main/install.sh | bash
```

It picks the build for your system and checks it against the release's checksums. It puts `heyrobotsd` in `~/.local/bin`, and on a Mac it clears the "downloaded from the internet" mark.

While the repository is private, an anonymous download can't reach it. Sign in with the GitHub CLI and run the script through it:

```bash
gh api repos/sdsvn/heyrobots/contents/install.sh -H "Accept: application/vnd.github.raw" | bash
```

Or use a token that can read the repository:

```bash
curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" https://raw.githubusercontent.com/sdsvn/heyrobots/main/install.sh | bash
```

| Setting | What it does |
|---|---|
| `HEYROBOTS_VERSION` | Install a given release, such as `v0.1.0`, instead of the latest |
| `HEYROBOTS_INSTALL_DIR` | Install somewhere else, such as `/usr/local/bin` |

For example, `curl -fsSL … | HEYROBOTS_VERSION=v0.1.0 bash`.

To install by hand instead, follow the steps below.

### On a Mac, by hand

```bash
tar -xzf heyrobotsd-*-darwin-arm64.tar.gz
cd heyrobotsd-*-darwin-arm64
xattr -c heyrobotsd
./heyrobotsd
```

The `xattr` line clears the "downloaded from the internet" mark, which macOS puts on unsigned programs and then refuses to start. It does nothing if the mark isn't there. On a Mac, the server keeps its vault key in your login Keychain.

### On Linux, by hand

```bash
tar -xzf heyrobotsd-*-linux-amd64.tar.gz
cd heyrobotsd-*-linux-amd64
./heyrobotsd
```

On Linux the vault key is kept in a file in the data folder, readable only by your user.

### Keep it running on Linux

To keep it running after you log out, run it as a systemd user service. The install script already puts the binary in `~/.local/bin`; by hand, copy it there:

```bash
mkdir -p ~/.local/bin && cp heyrobotsd ~/.local/bin/
```

Save this as `~/.config/systemd/user/heyrobots.service`:

```ini
[Unit]
Description=heyrobots

[Service]
ExecStart=%h/.local/bin/heyrobotsd
Environment=HEYROBOTS_LISTEN=127.0.0.1:7340
Restart=on-failure

[Install]
WantedBy=default.target
```

Then start it:

```bash
systemctl --user enable --now heyrobots
loginctl enable-linger "$USER"
```

### First start

The server prints a setup code in the terminal:

```
  heyrobots setup code: G8SSB-7X3E7
  Open http://127.0.0.1:7340/setup on this machine and enter it.
```

1. Open that address in a browser on the same machine and enter the code.
2. The page shows a pairing code and a QR code. Pair this browser with it, or scan the QR code with the app.
3. Pick a brain: who does the work, and who makes the quick routing calls.
   - **Your agent** uses an AI coding agent you already have on this machine and pay for: Claude Code, or [omp](https://omp.sh) with `HEYROBOTS_ACP_COMMAND`. You need no API key.
   - **An API key** uses Anthropic, OpenAI, DeepSeek or Gemini directly.
   - **A model on this machine** uses Ollama or LM Studio.

### Reaching it from your phone

By default the server listens only on its own machine (`127.0.0.1:7340`). To use it from a phone, also listen on an address the phone can reach:

```bash
HEYROBOTS_LISTEN="127.0.0.1:7340,192.168.1.110:7340" ./heyrobotsd
```

Use your machine's own network address in place of `192.168.1.110`. Your Mac prints it with `ipconfig getifaddr en0`, and Linux with `hostname -I`.

- **With Tailscale.** The server also listens on its tailnet address by itself. Only your own devices can reach that address, and it works away from home too.
- **Pairing codes.** Every code lists every address that works, the tailnet one first, and the app uses the first that answers.
- **`0.0.0.0` is refused.** It would listen on every network the machine is on. That is only allowed in public mode, behind an access proxy that checks every request.

### Faster replies with your own agent

Each reply is one or more turns of your agent's model, so the model decides the speed. A Sonnet model at low thinking answers in about 3–5 seconds. Opus takes about twice as long.

With omp:

```bash
HEYROBOTS_ACP_COMMAND="omp acp --model claude-sonnet-5-5 --thinking low --no-extensions" ./heyrobotsd
```

With Claude Code:

```bash
HEYROBOTS_AGENT_MODEL=claude-sonnet-5-5 ./heyrobotsd
```

### Several machines, one team

Each machine runs its own server and its own agents. Joined together, every agent shows up in every app, and the only difference is whether its machine is up.

1. On the machine others will dial, start the server with a machine link:
   ```bash
   HEYROBOTS_CLUSTER_LISTEN=192.168.1.110:7350 HEYROBOTS_MACHINE_NAME="Home Mac" ./heyrobotsd
   ```
2. In its app, open **Settings**, then **Your machines**, then **Add a machine**. It shows an address and a code.
3. On the other machine, open **Settings**, then **Your machines**, then **Join a machine**, and enter them.

A machine without a reachable address, such as a laptop, can still join. It only dials out. How the machines agree, and why they never vote, is explained in [design/architecture/heyrobots-communication.html](design/architecture/heyrobots-communication.html).

### Settings

| Variable | What it does | Default |
|---|---|---|
| `HEYROBOTS_LISTEN` | Addresses to listen on, separated by commas | `127.0.0.1:7340`, plus the tailnet address when there is one |
| `HEYROBOTS_DATA_DIR` | Where the database, keys and workspaces live | `~/Library/Application Support/heyrobots` on a Mac, `~/.config/heyrobots` on Linux |
| `HEYROBOTS_VAULT_KEY` | Where the vault key is kept: `keychain` or `file` | `keychain` on a Mac, `file` elsewhere |
| `HEYROBOTS_SERVER_URL` | The public address put first in pairing codes | — |
| `HEYROBOTS_TLS_CERT`, `HEYROBOTS_TLS_KEY` | Serve HTTPS with this certificate and key | plain HTTP |
| `HEYROBOTS_ACP_COMMAND` | The ACP agent to run as "Your agent", such as `omp acp` | Claude Code through `claude-agent-acp` |
| `HEYROBOTS_AGENT_MODEL` | Claude Code's model | Claude Code's own default |
| `HEYROBOTS_CLAUDE_CONFIG_DIR` | A separate Claude Code sign-in folder for heyrobots | your own `~/.claude` |
| `HEYROBOTS_CLUSTER_LISTEN` | Where this machine's link to your other machines listens | none; it only dials out |
| `HEYROBOTS_CLUSTER_ADDRESS` | The link address other machines dial, if it differs from the one above | — |
| `HEYROBOTS_MACHINE_NAME` | This machine's name in the app | the host name |
| `HEYROBOTS_PUBLIC` | `true` for public mode, which needs the `HEYROBOTS_GUARD_*` settings | off |
| `HEYROBOTS_MODEL_PRICES` | Prices for models the server doesn't know, as `model=in/out,…` in dollars per million tokens | built-in prices |
| `HEYROBOTS_DEBUG` | Any value logs every detail, including the agent's own output | off |

## Install the app

- **In a browser.** Open the server's address, such as `http://127.0.0.1:7340`. The server serves the app itself, so there's nothing to install.
- **On macOS.** Unzip `heyrobots-app-*-macos.zip` and move `heyrobots.app` to Applications. See [Opening the macOS app](#opening-the-macos-app) for the one extra step the first time.
- **On Linux.** Extract `heyrobots-app-*-linux-x64.tar.gz` and run `./heyrobots` inside the folder. It needs GTK 3, libsecret and a keyring service to keep its pairing, which most desktops have; on Debian or Ubuntu, `sudo apt install libgtk-3-0 libsecret-1-0 gnome-keyring`. There's no fingerprint check on Linux, so actions that need one are approved from your phone.
- **On Android.** Copy `heyrobots-app-*-android.apk` to the phone and open it. Android asks once to allow installs from that source.

Then pair the app. Every time the server starts, its terminal shows a QR code, the addresses to open and a one-time pairing code. Scan the QR code with the app, or open one of the addresses and enter the code. A device that's already paired can make another code under **Settings**, then **Add a phone**. You can also type the address and the code by hand.

Connecting an add-on, such as Gmail or Notion, opens a sign-in that returns to the server's own machine. So add apps from a browser or the desktop app on that machine. The phone app shows what's connected but doesn't start sign-ins.

Setting up Gmail and Google Calendar is in [docs/setup/google.md](docs/setup/google.md).

### Opening the macOS app

The first time you open it, macOS may say **"The application “heyrobots” can’t be opened."** The app isn't broken. It's built for both Apple silicon and Intel, and its signature is valid, but it's only signed locally (ad-hoc), not by Apple. Your browser also marks every download as quarantined, and macOS refuses a quarantined app that Apple hasn't signed.

Clear the mark once, then open it:

```bash
xattr -cr /Applications/heyrobots.app && open /Applications/heyrobots.app
```

Use `~/Downloads/heyrobots.app` instead if you haven't moved it.

Or, without the terminal: after the failed open, go to **System Settings**, then **Privacy & Security**. Scroll down to the line about "heyrobots" being blocked, click **Open Anyway**, and confirm.

Once it opens, pair it, then quit and reopen it to check it's still paired. An app that Apple hasn't signed sometimes can't keep its pairing in the Keychain. If it asks to pair again every time, open an issue.

Every new download needs this step once, until the app is signed and notarized (see [CI and releases](#ci-and-releases)).

## Build from source

You need Go (the version in `service/go.mod`) and Flutter 3.47 or newer.

```bash
make build    # the web app, then bin/heyrobotsd with it inside
make run      # build and start the server at http://127.0.0.1:7340
make test     # the server's and the app's tests
make dev      # serve app/build/web from disk while you work on the app
```

The apps:

```bash
cd app
flutter build macos --release   # needs Xcode
flutter build linux --release   # needs clang, cmake, ninja, GTK 3 and libsecret development packages
flutter build apk --release     # needs the Android SDK and Java 17 or newer
```

If Java isn't on your PATH for the Android build, point `JAVA_HOME` at a JDK, for example Homebrew's:

```bash
export JAVA_HOME=/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home
```

After changing `api/openapi.yaml`, regenerate both sides:

```bash
cd service/internal/api/gen && go generate ./...
cd app && flutter pub run swagger_parser
```

## CI and releases

Two GitHub Actions workflows live in `.github/workflows`:

- **`ci.yml`** runs on every push to `main` and every pull request. It vets and tests the server, and analyzes and tests the app.
- **`release.yml`** runs when you push a version tag. It builds every file in the Downloads table and attaches them, with checksums, to a GitHub release.

To make a release:

```bash
git tag v0.4.0
git push origin v0.4.0
```

To get the builds without making a release, run **Release** by hand from the Actions tab. The files appear as artifacts on that run.

The servers are built for every platform on one Linux runner, since they're pure Go. The macOS app is built on a macOS runner, and the Linux and Android apps on Linux runners.

Signing isn't set up yet:

- **macOS.** The server and the app are only signed locally (ad-hoc), not by Apple, and aren't notarized. So macOS refuses them until the quarantine mark is cleared, as described in [Opening the macOS app](#opening-the-macos-app). Signing and notarizing need an Apple Developer account, about $99 a year, with its Developer ID certificate and an app-specific password in the workflow's secrets.
- **Android.** The APK is signed with a debug key that each CI run makes afresh. It installs directly, but a newer release won't install over an older one: uninstall the old app first, which also unpairs it. The Play Store won't accept it either. Both need a fixed keystore in the workflow's secrets and a `signingConfig` that uses it in `app/android/app/build.gradle.kts`.

## Where things are

| Folder | What's in it |
|---|---|
| `service/` | The server, in Go |
| `app/` | The app, in Flutter |
| `api/openapi.yaml` | The API between them |
| `design/` | Design documents and architecture pages |
| `docs/setup/` | Setup guides for connected apps |
