<p align="center">
  <img src="docs/images/social-preview.png" alt="heyrobots: your own team of AI agents, running on your own machines" width="720">
</p>

<p align="center">
  <a href="https://github.com/sdsvn/heyrobots/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/sdsvn/heyrobots/actions/workflows/ci.yml/badge.svg?branch=main"></a>
  <a href="https://github.com/sdsvn/heyrobots/actions/workflows/release.yml"><img alt="Release build" src="https://github.com/sdsvn/heyrobots/actions/workflows/release.yml/badge.svg"></a>
  <a href="https://github.com/sdsvn/heyrobots/releases/latest"><img alt="Latest release" src="https://img.shields.io/badge/release-v0.4.2-5848E0"></a>
  <br>
  <a href="service/go.mod"><img alt="Go 1.26" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&amp;logoColor=white"></a>
  <a href="app/pubspec.yaml"><img alt="Flutter 3.47" src="https://img.shields.io/badge/Flutter-3.47-02569B?logo=flutter&amp;logoColor=white"></a>
  <a href="api/openapi.yaml"><img alt="OpenAPI 3" src="https://img.shields.io/badge/API-OpenAPI_3-6BA539?logo=openapiinitiative&amp;logoColor=white"></a>
  <a href="https://google.github.io/adk-docs/"><img alt="Google ADK" src="https://img.shields.io/badge/agents-Google_ADK-4285F4?logo=google&amp;logoColor=white"></a>
  <br>
  <img alt="Server: macOS, Linux" src="https://img.shields.io/badge/server-macOS_%7C_Linux-24213A?logo=apple&amp;logoColor=white">
  <img alt="App: macOS, Linux, Android, web" src="https://img.shields.io/badge/app-macOS_%7C_Linux_%7C_Android_%7C_web-24213A?logo=android&amp;logoColor=white">
  <img alt="Self-hosted" src="https://img.shields.io/badge/self--hosted-yes-1E7A62">
  <br>
  <a href="https://modelcontextprotocol.io"><img alt="MCP: any server" src="https://img.shields.io/badge/MCP-any_server-5848E0"></a>
  <a href="https://agentclientprotocol.com"><img alt="ACP: Claude Code, omp" src="https://img.shields.io/badge/ACP-Claude_Code_%7C_omp-D97757?logo=anthropic&amp;logoColor=white"></a>
  <a href="https://skills.sh"><img alt="Skills from skills.sh" src="https://img.shields.io/badge/skills-skills.sh-B4501E"></a>
  <a href="https://tailscale.com"><img alt="Works with Tailscale" src="https://img.shields.io/badge/works_with-Tailscale-242424?logo=tailscale&amp;logoColor=white"></a>
</p>

# heyrobots

**Your own team of AI agents, running on your own machines.** Chat with your agents from your phone, a browser or the Mac app. They work with your mail, calendar, files and apps. Anything that reaches other people waits for your OK.

- **A team, not one bot.** Each agent has its own job, tools, skills and model. Message one directly, or put several in a group chat where they answer, split the work and pass pieces to each other.
- **Runs on what you already pay for.** Use Claude Code or [omp](https://omp.sh) on your own subscription, an API key (Anthropic, OpenAI, DeepSeek, Gemini), or a model on your machine (Ollama, LM Studio).
- **Any app.** Connect from a starter set (Gmail, Calendar, Linear, Notion, GitHub, Jira, Stripe, Figma, Zapier and more), search the official MCP registry, or paste any MCP server. Add skills from [skills.sh](https://skills.sh). An IT agent can set this up for you.
- **Safe by default.** Outward actions (sending, posting, changing things) wait for your approval. Content from outside, such as mail or web pages, is read in quarantine and treated as untrusted, and agents can't change their own rules.
- **Always on, across your machines.** The server runs as a background service on a Mac, a Linux box or a cloud machine. Several machines can join into one team, and your phone is just a window onto it.
- **Your data stays yours.** Chats, memory and keys live in a folder on your machine. Keys go in an encrypted vault.

## Quick start

On a Mac or Linux machine:

```bash
curl -fsSL https://raw.githubusercontent.com/sdsvn/heyrobots/main/install.sh | bash
```

That one line:

1. **Installs the server,** `heyrobotsd`, in `~/.local/bin`, after checking it against the release's checksums.
2. **Runs it in the background,** starting at login: a launchd service on a Mac, a systemd user service on Linux.
3. **Installs the Mac app** in `/Applications`. Installed this way, macOS opens it without asking.
4. **Opens the app, already signed in.** On Linux, the web app opens in your browser, signed in.

Then pick a brain (Settings → Brain): your own agent, an API key, or a local model. Run the same line again to upgrade; it keeps your data.

<details>
<summary>The repository is private: installing with the GitHub CLI or a token</summary>

An anonymous download can't reach a private repository. Sign in with the GitHub CLI and run the script through it:

```bash
gh api repos/sdsvn/heyrobots/contents/install.sh -H "Accept: application/vnd.github.raw" | bash
```

Or use a token that can read the repository:

```bash
curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" https://raw.githubusercontent.com/sdsvn/heyrobots/main/install.sh | GITHUB_TOKEN="$GITHUB_TOKEN" bash
```

</details>

## How it fits together

```
 phone · browser · Mac app ──HTTPS──▶ heyrobotsd ◀──mutual TLS──▶ heyrobotsd on your other machines
                                        │
              ┌─────────────────────────┼──────────────────────────┐
              ▼                         ▼                          ▼
     your agents (ACP:          your apps (MCP:             model providers
     Claude Code, omp)          Linear, Notion, …)          (your keys)
```

- **The server, `heyrobotsd`,** holds your team, your chats, your keys and the web app. A lead decides which agent takes a message; each agent works in its own task folder with only the tools you allow.
- **The app** is the same Flutter app in a browser, on macOS, on Linux and on Android. It talks to one server at a time.
- **Several servers** share chats and hand work to each other's agents, with no voting: each fact has one writer. See [How heyrobots talks](design/architecture/heyrobots-communication.html).

## Using it

| You want to… | Do this |
|---|---|
| Talk to one agent | Open its chat from **Chats** or **Agents** |
| Get several agents on something | Start a group (the pencil on **Chats**). Write `both of you` or `everyone` to hear from each, or `@Name` for one |
| Add an app | **Settings → Add-ons**: tap one, or search the MCP registry, or paste a link. Or tell the IT agent what you use |
| Teach an agent a way of working | **Settings → Skills**: search skills.sh, add one, give it to an agent |
| Make something happen regularly | Say it in a chat: "every weekday at 8, brief me". The agent proposes a standing job; you confirm it |
| Approve or decline an action | The card in the chat, or **Plans → Needs you** |
| Change an agent | Its page on **Agents**, or just ask it ("from now on, …") |
| Delete a chat | Long-press or right-click it, or use its ⋯ menu |

Each agent works on up to 3 things at once by default (1–5), and one slot always stays free for your messages, so background jobs never make you wait.

## Reaching it from your phone

By default the server listens only on its own machine (`127.0.0.1:7340`).

- **With Tailscale,** the server also listens on its tailnet address by itself. Only your own devices can reach it, and it works away from home. This is the easiest way.
- **On your home network,** add the machine's address:
  ```bash
  HEYROBOTS_LISTEN="127.0.0.1:7340,192.168.1.110:7340" heyrobotsd
  ```
  Your Mac prints its address with `ipconfig getifaddr en0`; Linux with `hostname -I`.
- **Pairing:** every start prints a QR code, the addresses and a one-time code. Scan the QR code with the app; it tries each address and keeps the first that answers. A paired device makes more codes under **Settings → Add a phone**, and `heyrobotsd pair` prints one any time.

Signing in to an app such as Notion or Google comes back to the server's own machine, so add apps from a browser or the app on that machine. The phone shows them but doesn't start sign-ins. `0.0.0.0` is refused outside public mode.

## Faster replies with your own agent

Each reply is one or more turns of your agent's model, so the model sets the speed. Sonnet at low thinking answers in about 3–5 seconds; Opus takes about twice as long.

```bash
# omp
HEYROBOTS_ACP_COMMAND="omp acp --model claude-sonnet-5-5 --thinking low --no-extensions" heyrobotsd
# Claude Code
HEYROBOTS_AGENT_MODEL=claude-sonnet-5-5 heyrobotsd
```

The installer passes any `HEYROBOTS_*` setting on to the service:

```bash
curl -fsSL https://raw.githubusercontent.com/sdsvn/heyrobots/main/install.sh | HEYROBOTS_ACP_COMMAND="omp acp --model claude-sonnet-5-5 --thinking low --no-extensions" bash
```

## Several machines, one team

1. On the machine others will dial, give it a link address and a name:
   ```bash
   HEYROBOTS_CLUSTER_LISTEN=192.168.1.110:7350 HEYROBOTS_MACHINE_NAME="Home Mac" heyrobotsd
   ```
2. In its app: **Settings → Your machines → Add a machine**. It shows an address and a code.
3. On the other machine: **Settings → Your machines → Join a machine**, and enter them.

A laptop without a reachable address can still join; it only dials out. Every agent shows up in every app, and the only difference is whether its machine is up.

## Day to day

| | On a Mac | On Linux |
|---|---|---|
| Log | `~/Library/Logs/heyrobots.log` | `journalctl --user -u heyrobots` |
| Restart | `launchctl kickstart -k gui/$(id -u)/dev.heyrobots.heyrobotsd` | `systemctl --user restart heyrobots` |
| Is it running | `heyrobotsd status` | `heyrobotsd status` |
| New pairing code | `heyrobotsd pair` | `heyrobotsd pair` |

**Uninstall** on a Mac:

```bash
launchctl bootout gui/$(id -u)/dev.heyrobots.heyrobotsd; rm -f ~/Library/LaunchAgents/dev.heyrobots.heyrobotsd.plist ~/.local/bin/heyrobotsd; rm -rf /Applications/heyrobots.app
```

On Linux:

```bash
systemctl --user disable --now heyrobots; rm -f ~/.config/systemd/user/heyrobots.service ~/.local/bin/heyrobotsd
```

Your data stays in its folder (`HEYROBOTS_DATA_DIR`); delete it too to remove everything.

## Settings

The installer's own settings:

| Setting | What it does |
|---|---|
| `HEYROBOTS_VERSION` | Install a given release, such as `v0.4.0`, instead of the latest |
| `HEYROBOTS_INSTALL_DIR` | Put `heyrobotsd` somewhere else, such as `/usr/local/bin` |
| `HEYROBOTS_APP_DIR` | Put the Mac app somewhere else |
| `HEYROBOTS_NO_SERVICE=1` | Install the server only, and run it yourself |
| `HEYROBOTS_NO_APP=1` | Don't install or open the app |

The server's:

| Variable | What it does | Default |
|---|---|---|
| `HEYROBOTS_LISTEN` | Addresses to listen on, separated by commas | `127.0.0.1:7340`, plus the tailnet address |
| `HEYROBOTS_DATA_DIR` | Where the database, keys and workspaces live | `~/Library/Application Support/heyrobots` on a Mac, `~/.config/heyrobots` on Linux |
| `HEYROBOTS_VAULT_KEY` | Where the vault key is kept: `keychain` or `file` | `keychain` on a Mac, `file` elsewhere |
| `HEYROBOTS_SERVER_URL` | A public address, put first in pairing codes | — |
| `HEYROBOTS_TLS_CERT`, `HEYROBOTS_TLS_KEY` | Serve HTTPS with this certificate and key | plain HTTP |
| `HEYROBOTS_ACP_COMMAND` | The agent to run as "Your agent", such as `omp acp` | Claude Code through `claude-agent-acp` |
| `HEYROBOTS_AGENT_MODEL` | Claude Code's model | Claude Code's own default |
| `HEYROBOTS_CLAUDE_CONFIG_DIR` | A separate Claude Code sign-in folder for heyrobots | your own `~/.claude` |
| `HEYROBOTS_CLUSTER_LISTEN` | Where this machine's link to your other machines listens | none; it only dials out |
| `HEYROBOTS_CLUSTER_ADDRESS` | The link address other machines dial, if different | — |
| `HEYROBOTS_MACHINE_NAME` | This machine's name in the app | the host name |
| `HEYROBOTS_PUBLIC` | `true` for public mode, which needs the `HEYROBOTS_GUARD_*` settings | off |
| `HEYROBOTS_MODEL_PRICES` | Prices for unknown models, as `model=in/out,…` in dollars per million tokens | built-in prices |
| `HEYROBOTS_DEBUG` | Any value logs every detail, including the agent's own output | off |

## Downloads

Every release on the **Releases** page has these files (`vX.Y.Z` is the version):

| File | What it is |
|---|---|
| `heyrobotsd-vX.Y.Z-darwin-arm64.tar.gz` / `-darwin-amd64` | Server for Apple silicon / Intel Macs |
| `heyrobotsd-vX.Y.Z-linux-amd64.tar.gz` / `-linux-arm64` | Server for Linux on x86-64 / ARM (such as a Raspberry Pi 4 or 5) |
| `heyrobots-app-vX.Y.Z-macos.zip` | App for macOS |
| `heyrobots-app-vX.Y.Z-linux-x64.tar.gz` | App for Linux on x86-64 |
| `heyrobots-app-vX.Y.Z-android.apk` | App for Android |
| `SHA256SUMS.txt` | Checksums for every file above |

Check a download before you use it: `shasum -a 256 -c SHA256SUMS.txt --ignore-missing`.

<details>
<summary>Installing by hand</summary>

**Server on a Mac:**

```bash
tar -xzf heyrobotsd-*-darwin-arm64.tar.gz && cd heyrobotsd-*-darwin-arm64
xattr -c heyrobotsd   # clears the "downloaded from the internet" mark macOS puts on unsigned programs
./heyrobotsd
```

**Server on Linux:**

```bash
tar -xzf heyrobotsd-*-linux-amd64.tar.gz && cd heyrobotsd-*-linux-amd64
./heyrobotsd
```

To keep it running after you log out, copy it to `~/.local/bin` and save this as `~/.config/systemd/user/heyrobots.service`:

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

```bash
systemctl --user enable --now heyrobots
loginctl enable-linger "$USER"
```

**First start.** The terminal prints a QR code, the addresses to open and a one-time code. Open `http://127.0.0.1:7340` on the same machine and enter the code, then pick a brain.

**The apps:**

- **Browser:** open the server's address. The server serves the app; nothing to install.
- **macOS:** unzip `heyrobots-app-*-macos.zip`, move `heyrobots.app` to Applications, then clear the download mark once: `xattr -cr /Applications/heyrobots.app && open /Applications/heyrobots.app`. (Or after the first failed open: **System Settings → Privacy & Security → Open Anyway**.) The app isn't signed by Apple yet, which is why this step is needed.
- **Linux:** extract `heyrobots-app-*-linux-x64.tar.gz` and run `./heyrobots`. It needs GTK 3 and libsecret (`sudo apt install libgtk-3-0 libsecret-1-0` on Debian or Ubuntu). There's no fingerprint check on Linux, so actions that need one are approved from your phone.
- **Android:** copy the `.apk` to the phone and open it; Android asks once to allow installs from that source.

The desktop apps keep their pairing in a file only you can read; phones use the Keychain or Keystore.

Setting up Gmail and Google Calendar is in [docs/setup/google.md](docs/setup/google.md).

</details>

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
flutter build linux --release   # needs clang, cmake, ninja and GTK 3 development packages
flutter build apk --release     # needs the Android SDK and Java 17 or newer
```

If Java isn't on your PATH, point `JAVA_HOME` at a JDK, for example Homebrew's: `export JAVA_HOME=/opt/homebrew/opt/openjdk/libexec/openjdk.jdk/Contents/Home`.

After changing `api/openapi.yaml`, regenerate both sides:

```bash
cd service/internal/api/gen && go generate ./...
cd app && dart run swagger_parser
```

## CI and releases

- **`ci.yml`** runs on every push to `main` and every pull request: it vets and tests the server, and analyzes and tests the app.
- **`release.yml`** runs when you push a version tag (`git tag v0.4.1 && git push origin v0.4.1`). It builds every file in the Downloads table and attaches them, with checksums, to a GitHub release. Run it by hand from the Actions tab to get the builds without a release.

Not set up yet:

- **macOS signing.** The server and the app are signed only locally (ad hoc), not by Apple, and aren't notarized. That needs an Apple Developer account and its Developer ID certificate in the workflow's secrets.
- **Android signing.** Each CI run signs the APK with a fresh debug key, so a newer release won't install over an older one: uninstall first (which also unpairs it). A fixed keystore in the workflow's secrets fixes that and lets it go on the Play Store.

## Where things are

| Folder | What's in it |
|---|---|
| `service/` | The server, in Go |
| `app/` | The app, in Flutter |
| `api/openapi.yaml` | The API between them |
| `design/` | Design documents and architecture pages |
| `docs/` | Setup guides and images |
