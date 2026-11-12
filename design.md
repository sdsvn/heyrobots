# heyrobots: a proactive personal assistant you run yourself

heyrobots is a personal assistant that anyone can run on their own computer or server. You ask it for work from its phone or desktop app, by email, or from apps you connect such as Slack and GitHub. It works in the background and comes back only when you must act or can use a result. It can also watch the accounts you connect and take on work you wrote a rule for. That work starts in a mode that only records what it would have done.

The first release is **self-hosted**. Each install serves one owner, on hardware they control, using their own model API key and their own account connections. A hosted service for people who don't want to run anything comes later (section 14). The design keeps the parts that service will need.

The design is based on DeepSeek's [dsh-superagent prototype](https://github.com/deepseek-ai/dsh-superagent-prototype), a proactive coding agent for one team's GitHub repository. It keeps that prototype's domain model and authorization rules, and uses its terms. It changes four things:
- the assistant serves one person;
- anyone can install it, not just one team;
- the work is not only code;
- the agent runs as a Go service on Google's Agent Development Kit (ADK), with a Flutter app as its client.

`build-superagent-on-adk-prompt.md` is the original brief. Self-hosted apps such as Home Assistant are the model for distribution: one open-source server you run, and one companion app from the stores that pairs with it.

## 1. Principle

The prototype's principle applies unchanged: **take as little human attention as possible, and give as much value as possible.** A personal assistant sees your inbox and calendar, so it could easily become one more source of noise.

- It acts only when you ask, or when a rule you wrote allows it.
- It confirms a request once, stays quiet while it works, and writes again only for a result, a question or a failure.
- It asks one concrete question at a time.
- It reports honestly. A draft is marked as a draft, and a failure is reported as a failure.
- Proactive results go into a daily digest by default rather than separate notifications.
- Setup asks for as little as possible. A new user can get value from the app chat alone, before connecting any account.

## 2. Rules kept from the prototype

- **Code decides authority, never the model.** A deterministic gate runs before any model call. The model interprets requests and routes them, but it cannot grant permission.
- **Outside text is data.** Emails, messages, documents and web pages never change rules, allowed lists or scope.
- **A Ticket is not a session.** A Ticket is durable work with the lifecycle `open → ended → open`. An ADK session is one attempt at it.
- **Every Ticket has an immutable Mandate.** It records the policy version, scope, strength, matched rules, requester identity and how the request arrived.
- **Idempotent intake.** Each event is stored once. One Eval runs per Thread at a time, and events that arrive meanwhile are batched into the next one.
- **Transactional outbox.** Every outgoing action is written in the same transaction as the decision behind it and sent later with retry and ordering.
- **Observe mode first.** Proactive work is assessed and recorded as "would accept" until the owner switches to `auto`.
- **Executors never ask people directly.** They return one `human_action`, and the host delivers it.

## 3. Terms

These are the prototype's terms. Only **Assistant** and **Owner** are new.

| Term | Meaning here |
|---|---|
| **Assistant** | One person's agent, with its own Sources, Policy, Tickets, memory, files and credentials |
| **Owner** | The person an Assistant serves. Every Assistant has exactly one, and a self-hosted install has exactly one Assistant |
| Source | A stream of events, such as the app chat, the owner's mailbox, a connected calendar, a GitHub repository or a schedule |
| Trigger | One stored event |
| Thread | Triggers in time order that the Source treats as one conversation, such as an email thread, a Slack thread or an issue |
| Channel | The write side of a Thread, used for replies, drafts, pull requests and invites |
| Policy | Who may ask the Assistant for work, its proactive rules, accept mode and standing approvals, all versioned |
| Eval | The gate plus the router's judgement on a batch of Triggers |
| Mandate | What an accept grants |
| Ticket | A unit of authorized work |
| Assignment | One Executor attempt at a Ticket |
| Executor | An ADK worker agent with a toolset, a workspace and limits |
| Subscription | How later messages on a Thread reach its Tickets without a new mention |
| Effect | An outgoing action held in the outbox |

As in the prototype, a person is not a domain concept. Each Source's part of the Policy lists identities in that Source's own terms. The service never guesses that two accounts belong to the same person, because a wrong guess would hand out authority.

## 4. Who can ask an Assistant for work

| Sender | Example | Strength |
|---|---|---|
| The owner, addressing the Assistant | app chat from a paired device; an email from the owner's own address to the Assistant's address; a Slack DM to the bot from the owner's linked Slack user | explicit |
| A delegate the owner invited, addressing the Assistant | a partner who may ask yours to add things to the family calendar | explicit, limited to the toolsets the owner allowed that delegate |
| Anyone else | an email in your inbox, a message in a connected channel, an invite, a review request | never explicit; data that can become an inferred candidate under the owner's rules |

**The owner's identities are proven, never assumed.** The first paired device belongs to the owner. Other identities are linked from the app by a check the owner completes:
- An email address is linked by connecting that mailbox, or by a confirmation code sent to it.
- A Slack or GitHub account is linked by signing in with it.

**The Assistant's email address.** By default the Assistant reads a plus-address of the owner's own mailbox, such as `you+assistant@gmail.com`. Mail sent there from the owner's own address, passing DKIM alignment for that domain, is an explicit request. Anything else that arrives there is "anyone else". A `From:` line alone is never trusted, because it is easy to forge.

**Delegates are invited.** The owner creates an invite in the app. The delegate pairs their own copy of the app with it and gets a separate, limited device token.

**Only the owner approves.** Actions run on the owner's accounts, so every approval of an `others` or `irreversible` action goes to the owner, including actions a delegate asked for. A delegate's token can never approve anything. The delegate sees that the action is waiting for the owner.

Messages from other people never reach the gate as requests. They reach the Assistant only as material for rules the owner wrote, under observe mode first. Anyone can email you, so this is the main defence against prompt injection.

## 5. Acting as the owner

A personal Assistant can act *as you*. It can send from your mailbox, answer your invites and post as you. So every Effect carries a **reach**, which code checks.

| Reach | Examples | Rule |
|---|---|---|
| `internal` | files in the workspace, notes, memory, drafts | allowed |
| `owner` | a reply to you in the app, an email to your own address, a push | allowed |
| `requester` | a reply to a delegate in their own conversation in the app; it is sent by the assistant, never as the owner | allowed |
| `others` | anything that reaches another person: an email, an invite reply, a message, a comment, a pull request | needs your approval, unless a standing approval covers it |
| `irreversible` | spending money, deleting data outside the workspace, publishing publicly | always needs your approval, every time, in every mode |

- **Standing approvals are narrow rules in the Policy.** They name the Source, the kind of action and the specific people they cover. For example: "accept calendar invites from these three people when they fit my working hours". They are versioned with the Policy and recorded in the Mandate. A standing approval can never cover `irreversible`.
- **Approval happens in the app.** It shows the full content of the action. On a phone it then asks for Face ID or a fingerprint.
- **An answer is part of the record.** Approving or rejecting is stored as a message from the owner in the action's conversation, with the device that answered. A rejection reaches the task, with the owner's note, so it can change course. Its record names no recipient: the owner's own messages count as trusted for later actions, and a declined recipient must never become trusted that way.
- **Approval isn't the last check.** Just before anything is sent, the outbox checks again: a stopped task sends nothing, an action whose Mandate no longer holds under the current Policy is dropped and the owner is told, and an action that went ahead under a standing approval goes back for approval if that standing approval was removed.
- **Taint rule.** Untrusted content means web pages, emails from anyone but the owner, and documents the owner didn't write. Nearly every useful task reads some of it, such as replying to an email. Requiring fresh approval for any action after any untrusted read would cancel almost every standing approval. So the rule looks at where an action's *details* came from, following the CaMeL paper's idea that untrusted content may shape what the agent says but can't decide where it goes or what it does.
  - **Controlling details** are the parts of an action that decide its effect: recipients and destinations, times and dates, amounts, and links or attachments to include.
  - **Trusted sources** for those details are the owner's request, the standing approval itself, the owner's memory, the owner's address book, and people the owner has already written to in that thread.
  - **The check.** Before an `others` action runs, a `BeforeToolCallback` compares each controlling detail with the trusted sources. If any detail appears in no trusted source, the action needs fresh approval even when a standing approval would cover it. This is stricter than looking only for details that came from untrusted content: a detail the model made up is caught too. That covers a new recipient named in an email, a link to a domain nobody trusted, or a date that only an outside message gave. The approval screen marks which detail came from where.
  - **Free text is allowed.** The body of a message may draw on untrusted content, because its recipients are already fixed by trusted sources. This still lets a trusted recipient see what the agent wrote, which is why standing approvals name specific people.
  - **Quarantined reading.** Untrusted content reaches the Executor through a separate model call that has no tools. It returns structured fields, each labelled with its source, and the Executor sees those fields rather than raw text. This is what lets the check know where each detail came from.
  - This is enforced in code, not in the prompt.
- **Send limits.** Every Assistant has a daily cap on `others` actions and on new recipients. This keeps a hijacked or misused Assistant from becoming a spam tool.

## 6. Proactive work

The owner writes rules in plain words. Each rule describes a kind of work the Assistant may start without being asked. Accept mode works as in the prototype: `off`, `observe` (the default) and `auto`. In `observe`, the app shows what the Assistant would have done, so you can tune a rule before switching it on.

Starter rules, offered as templates and off by default:
- "Each weekday at 8:00, give me a one-screen brief of today's calendar and anything urgent in my inbox."
- "Thirty minutes before a meeting, prepare a short note on who's attending and our last thread."
- "When a pull request requests my review, summarise it and flag risky parts."
- "When I forward a bill, add the due date to my calendar and remind me two days before."

Time-based work comes from a **schedule Source**. A scheduled Trigger carries the Mandate of the owner who created the schedule, never a new authority.
- **How a schedule speaks.** A firing speaks as the schedule's creator but doesn't address the Assistant. The gate therefore treats it as proactive work: it needs an enabled rule that covers it, and accept mode decides whether that work starts or is only noted. The gate judges each firing against the Policy at that moment, so a schedule made by someone who has since lost access starts nothing.
- **Each firing is its own conversation.** If firings shared a conversation, the next one would look like the owner following up on the last one's task. That would make it an explicit request and skip the rules.
- **Missed times.** Only the most recent missed time fires, and only within six hours of it. A server that was off for a week doesn't send a week of briefs, and a schedule never fires for a time before it was created.
- **Starter rules are templates.** Adding one creates a normal rule, plus a schedule if the rule is time-based. Accept mode is left as it is, so in the default observe mode a new rule only notes what it would do. A template is offered only once the account it needs is connected.
- **No rule, no cost.** An inferred candidate that no enabled rule could cover, and that isn't a follow-up on a task, is recorded without asking the router. Otherwise every email in a connected inbox would cost a model call.

## 7. Toolsets

A toolset is an attribute of an Executor. The router chooses toolsets when it accepts a Ticket, and the Mandate records them.

The gate runs before the router, so it can't yet know which toolsets the router will pick. A second check in plain code therefore runs inside `route_accept`, before the Ticket and Mandate are written. It refuses any toolset the requester may not use, and the Ticket is not created. The router can then accept with fewer toolsets or ignore the request. The same check runs again whenever an Executor asks to add a toolset to a live Ticket.

| Toolset | Tools | Where it runs |
|---|---|---|
| research | web search, web fetch, notes | sandbox |
| writing | files, document templates | sandbox |
| calendar | read events, propose events, answer invites | server, through the provider's API |
| mail | read and search mail, write drafts, send after approval | server |
| data | CSV and spreadsheet analysis, charts | sandbox |
| code | shell with a blocked-command list, files, git, `gh` | sandbox with a git worktree |

Each toolset has a prompt file. Adding one means adding a Go package and a prompt, without touching the core. What counts as a good result for each kind of work is prompt text, not a domain type.

### Sandboxing
Executors run commands and touch files on the owner's own machine, so the sandbox protects that machine from the agent and from whatever it reads.
- **With Docker or OrbStack installed**, which is recommended, every toolset that runs commands or touches files gets a throwaway container per Assignment. The container mounts only that Ticket's workspace. It reaches the network only through a proxy that applies an allowlist per toolset and logs every request. CPU, memory, disk and time are limited.
- **Without a container runtime**, those toolsets are limited the way the prototype limits them. Work happens only inside the Ticket's workspace folder, a blocked-command list applies, and there is no `sudo`.
- **That fallback has no real network limit.** Proxy settings passed through environment variables are only a request, and any program can ignore them. In this mode the agent's commands can reach anything the owner's machine can reach. The app says that plainly, with a persistent warning, and the `code` and `data` toolsets stay off until the owner turns them on knowingly. Research and writing still work, because their web access goes through the server's own fetch tools, which the proxy does control.
- Secrets never enter the sandbox. Tools that need tokens run in the service, outside the sandbox.

## 8. Connected accounts

Connecting accounts is optional and gradual. A new owner gets value from the app chat alone.

Because each install serves one person, the owner registers their own OAuth app with each provider. A setup guide in the app walks them through it. That avoids most provider reviews:
- Google's [personal-use exception](https://support.google.com/cloud/answer/13464323) lets an app skip verification, even for restricted Gmail scopes, when it isn't shared or has fewer than 100 users the developer knows personally. Gmail and Calendar therefore work with full read and draft access. This was checked against Google's documentation on 2026-11-10.
  - The consent screen shows an ["unverified app" warning](https://support.google.com/cloud/answer/7454865) that the owner clicks through, and the setup guide says why.
  - The 100-user cap covers the project's whole lifetime and can't be reset. That's ample for one owner and a few delegates.
  - Refresh tokens can expire after 7 days while the app is in "Testing" status, so the guide sets the app to "In production" for the owner's own use.
  - Google can change these rules. The setup guide links to the policy, and the server reports a clear error if Google starts refusing the scopes.
- Microsoft 365 and Outlook accounts work through an app registered in the owner's own Microsoft Entra tenant.
- Slack works through an app installed only in the owner's workspace.
- GitHub works through a GitHub App the owner creates and installs on chosen repositories.

| Connection | Gives the Assistant |
|---|---|
| Mailbox (Gmail, Microsoft 365, or any IMAP and SMTP server) | the Assistant's address, inbox reading, drafts, and sending after approval |
| Calendar (Google, Microsoft, or CalDAV) | events, proposals and invite replies |
| Slack | DMs and mentions in the owner's workspace |
| GitHub | issues, pull requests, reviews and CI on chosen repositories |

- Scopes start minimal, and sending is a separate permission the owner grants only when they want approved sends.
- Tokens are encrypted in the vault and used only by tool code in the service. The model never sees a token.
- Disconnecting an account stops its Source and revokes the token with the provider.

### Google
The full owner-facing steps are in `docs/setup/google.md`.
- **The owner's own client.** The owner creates a Desktop app OAuth client in their own Google Cloud project. A Desktop client accepts a redirect to `http://127.0.0.1` on any port, so the server needs no public address for sign-in. Sign-in uses PKCE and a one-time state that expires after 15 minutes.
- **Finishing the sign-in.** On the server's own computer, the redirect reaches the server and finishes by itself. On a phone, the browser ends on a page that can't load there; the owner pastes its address into the app, which finishes the same way. The callback needs no device token, because only a sign-in an owner device started can match its state.
- **Two levels of permission.** Reading and drafting use `gmail.readonly`, `gmail.compose` and `calendar.readonly`. Sending mail and changing events add `gmail.send` and `calendar.events`. The senders refuse permanently without them, telling the owner to connect again.
- **Connecting proves the address.** Connecting adds the account's address to the owner's identities on the mail and calendar Sources, as a new Policy version. Disconnecting removes it.
- **Polling.** The mail Source reads Gmail's history every minute, starting from the moment of connecting, at most 200 messages per poll. The calendar Source polls every two minutes. A history position or sync token Google has dropped restarts from now.

### The mail Source
- **The Assistant's address** is a plus-address of the owner's own mailbox. Mail to it addresses the Assistant.
- **Proof of the sender.** A message counts as verified when a valid DKIM signature from the From address's own organizational domain covers it and signs the From header (relaxed alignment). A signature that covers only part of the body, through the `l=` tag, doesn't count, since text added after the signed part would pass.
  - Mail the owner sends to themselves inside Gmail doesn't always carry a DKIM signature. Mail filed as sent from the connected account itself therefore also counts as the owner's, since only someone signed in to the account can send it.
  - Anything else is unverified, whatever its From line says.
- **What becomes a Trigger.** Mail to the Assistant's address, and other mail in the inbox, which arrives because the inbox receives everything. Spam, trash, drafts and mail the Assistant sent itself are skipped. The Assistant's own mail carries an `X-Heyrobots-Effect` header.
- **Replies to a request by mail** go back in the same thread, from the owner's account. To the owner that is reach `owner`. To a delegate it would act as the owner, so it is reach `others` and waits for the owner's approval.
- A forwarded message counts as the owner's own words. Forwarding something is vouching for it.

### Mail and calendar toolsets
- **Reading through quarantine.** `search_mail` returns only message ids, threads, sender addresses and dates, picked out by plain code. Subjects and text go through the quarantined reader, as do `read_mail` and `read_event`. `list_events` shows titles only for events the owner organized.
- **Direct and queued tools.** `draft_email` runs directly, because a draft reaches nobody. `send_email`, `create_event` and `answer_invite` are outward tools that only queue an Effect, with the details the sender needs kept as its payload.
- **Reach.**
  - Mail only to the owner's own proven addresses is reach `owner`.
  - An event with no attendees reaches only the owner.
  - Answering an invitation names its organizer as the recipient, so a standing approval can cover replies to specific people.
- **Dates are checked strictly.** Event times are controlling details, compared word for word with the owner's own messages, so an event usually needs approval unless the owner wrote its exact time. This fails closed.
- **Senders.** Gmail can't tell a repeated send from a new one, so a mail send cut off by a restart is marked uncertain. A calendar event's id comes from its Effect, and answering an invitation twice gives the same answer, so calendar sends are retried safely.

## 9. Memory

- The Assistant remembers its owner's preferences and facts across Tickets, using ADK's memory service.
- Memory is written only through an explicit `remember` action, never from untrusted content.
- The owner can see and delete every entry in the app.

## 10. Privacy and cost

Self-hosting keeps the owner's data on their own hardware, and the design keeps it that way.

- **Local data.** The database, workspaces, memory and vault live in one data folder. Nothing is sent anywhere except to the model provider and the accounts the owner connected.
- **No telemetry by default.** Crash reports and update checks are opt-in.
- **Encryption.** The vault is encrypted with a key held in the operating system's keychain on a Mac. On Linux it is held in a key file readable only by the service user. Full-disk encryption is recommended in the setup guide.
- **Model provider.** The owner brings their own Gemini API key. The setup guide recommends paid API terms, under which Google doesn't use the data for training. A local model through an OpenAI-compatible endpoint is a later option for owners who want nothing to leave the machine.
- **Cost control.** The owner sets a monthly model budget, $20 by default. Every model call's tokens are recorded against its Ticket and priced from a table the owner can change (`HEYROBOTS_MODEL_PRICES`); a model with no known price is estimated high on purpose, so the budget stops early rather than late. Once the month's budget is used up, an Executor stops before its next model call and asks the owner to raise it; a reply after the raise carries on. Routing isn't stopped, so the owner can still stop or redirect work, but it counts toward the budget. The app shows spending per Ticket and for the month.
- **Retention.** Ended Tickets and their sessions are deleted after a set period, 90 days by default. Memory stays until the owner deletes it.
- **Export and wipe.** The app can export everything as a single archive, or wipe the data folder and revoke every connected token.
- **Send caps** protect the owner's own mailbox and accounts from being flagged if something goes wrong. By default each Source may send 10 things that reach other people per hour and 50 per day. A send over the cap waits for the window to pass rather than failing. Messages to the owner in the app aren't capped.

## 11. The server and the app

```
              ┌───────────────────── heyrobotsd (on the owner's Mac or server) ──────────────────────┐
Sources ────▶ │ intake → gate → router → executors → outbox     vault     SQLite     HTTPS API + SSE │
              └───────────────┬──────────────────────────────────────────────────────────▲───────────┘
                              │ container per Assignment                                 │ device token
                        Docker / OrbStack              Flutter app (iPhone, Android, Mac, Windows)
                                                        over a private network, or a guarded public address
                                                        push through ntfy
```

### The server
- `heyrobotsd` is a single Go binary. It installs through Homebrew on a Mac, as a Docker image, or as a plain binary with a `systemd` unit on Linux.
- On a Mac it runs under `launchd` and starts at login. It pauses while the Mac sleeps and catches up when it wakes. An always-on machine, such as a Mac mini, a home server or a small cloud VM, avoids the pause.
- The first run opens a setup page on `localhost`. It asks for the model API key, shows a QR code to pair the first device, and offers the account setup guides. It asks nothing else.
- **The setup page never trusts the source address.** Tunnels and reverse proxies deliver requests from `127.0.0.1`, so "it came from localhost" proves nothing. Setup needs a one-time code that the server prints in the terminal and its log. All setup endpoints switch off once the first device is paired.
- Upgrades run database migrations automatically. A backup of the data folder is taken first.

### Reaching the server from the phone
- **Private by default.** The server listens only on the machine itself. The recommended way to reach it away from home is a private network such as Tailscale, WireGuard or a home VPN. No particular provider is assumed.
- **Public access is opt-in and always guarded.** If the owner exposes the server on a public address, it must sit behind an access proxy that signs in the owner first. Any OIDC-based proxy works, such as Cloudflare Access, Pomerium or oauth2-proxy. The owner configures the proxy's issuer and key in heyrobots.
- **The server checks the guard itself.** In public mode it rejects every request that lacks a valid signed identity from the configured proxy, and that identity must be the owner's. A missing or misconfigured proxy therefore fails closed.
- **The device token is always required too.** A private network or a proxy sign-in alone is never enough.
- The API never listens on all interfaces unless the owner turns on public mode.

### Pairing and devices
- The setup page shows a QR code holding the server's address and a one-time code that expires after ten minutes. The app scans it and receives a device token. Only a hash of the token is stored.
- A paired device can show a QR code to pair another. Devices are listed in the app and can be revoked there.
- Delegates pair with an invite code that creates a limited delegate token. A delegate token can request work within its toolsets, but it can never approve an action.
- Approving an `others` or `irreversible` action needs Face ID, Touch ID or a fingerprint on the device. A passcode isn't enough, because someone who saw it typed could approve actions on the owner's accounts.
- The server can't check a face or a fingerprint itself. The app sends `user_verified` with the approval, the server refuses such an approval without it, and the record of the approval says it was confirmed. This holds every app version to asking, but it is not proof against a stolen device token. A device that can't check biometrics, such as a browser, can still decline actions and approve messages to the owner.

### The API
- JSON over HTTPS for reads and actions, and server-sent events for live updates. Every app uses the same endpoints.
- An OpenAPI file is the single source for the generated Go server stubs and Dart client. The Dart client is generated by `swagger_parser` as plain `package:http` code with no build step; `dart run swagger_parser --check` fails when it is out of date.
- `GET /attention` lists what waits on a person: actions to approve, questions from tasks, and sends that failed. A delegate sees only questions on their own conversations. A failed send can be dismissed once read; approvals and questions go away only when answered.
- Chatting or approving through the API is recorded as a Trigger on the app Source, so it passes through the same gate and audit trail as everything else.

### The Flutter app
- One codebase for iOS, Android, macOS and Windows. It is a companion app that works with any heyrobots server it is paired with.
- The phone screen shows what waits on you first: approvals, questions and failures. Then it shows the digest and the chat.
- Settings cover rules, accept mode, standing approvals, connected accounts, memory, schedules, delegates, devices, budget, export and wipe.
- **Pairing.** Phones scan the QR code; desktops enter the address and code by hand. Plain http is accepted only for this machine and private networks (home networks, `.local`, and 100.64.0.0/10 as used by Tailscale), so the device token never crosses the internet unencrypted. The token is kept in the Keychain on Apple devices and the Keystore on Android.
- **Live updates** come from server-sent events. Where a response can't be streamed, as in a browser, the app polls instead.
- **Links.** Pushes open `heyrobots://thread/ID`, `heyrobots://approval/ID`, `heyrobots://digest` or `heyrobots://home`.
- **The browser build** is for development only. It keeps its pairing in memory and can't approve actions that need biometrics.
- **Distribution.** Early builds go out as an Android APK, a notarized Mac app and iPhone TestFlight builds. Publishing to the App Store and Google Play comes once the app is stable. Apple needs a paid developer account for both TestFlight and the App Store.

### The outbox
- **One queue for everything that leaves.** Replies, notices and outward actions are Effects, written in the same transaction as the decision behind them. The outbox sends them later.
- **Order per conversation.** An Effect waits while an older one in its conversation is still being sent or retried. Actions waiting for approval and results waiting for the digest don't hold others up.
- **Retries.** A failed send is retried with a growing wait, from 30 seconds up to an hour, eight times in all. An error the Source marks permanent fails at once. A failure is reported to the owner in the app's notices conversation, which names the action but doesn't quote its content.
- **Crash recovery.** A send that was cut off by a restart is tried again only through a Source that drops duplicates. Through any other, nobody can tell whether it went out, so it is marked uncertain and the owner is asked to check rather than risk sending it twice. Evals and Assignments recover through their leases, and an interrupted Assignment restarts with a note to check what it already did.
- **Stopping a task** cancels its running Assignment at once and cancels its actions that haven't been sent. The confirmation of the stop is still sent.
- **The daily digest.** Proactive results are held and put together once a day at the owner's digest hour, 08:00 in the owner's time zone by default. An empty digest isn't made.

### Notifications
- Push goes through **ntfy**. It works without the app publisher's Apple or Google push keys, which a self-hosted server can't hold. The owner can use ntfy.sh with a private topic and access token, or run ntfy themselves.
- A push holds only a short title and a link that opens the companion app on the right Ticket. Email text, results and other people's names never go in a push.
- Quiet hours and a per-hour limit are on by default: no pushes from 22:00 to 07:00 in the owner's time zone, and at most six an hour.
- **No queue of its own.** The notifier reads what waits on the owner and the latest digest, pushes whatever hasn't had a push, and records the keys. Pushes held back by quiet hours or the limit are therefore not lost: once allowed, everything that waited goes out as one push, such as "3 things wait for you". A failed push is tried again a minute later.
- The settings are part of the Policy, so each change is a new version. The access token is kept in the vault and never returned by the API.

## 12. Stack

| Need | Choice |
|---|---|
| Agents, runner, callbacks, tools, memory | `google.golang.org/adk` v1.9.0 |
| Conversation history | ADK `session/database`, which takes any GORM driver |
| Domain tables | GORM models in the same database |
| Database | SQLite through a pure-Go driver by default, so the server stays one binary with nothing else to run; Postgres as a setting for larger installs and the default for a hosted service. Bolt and other key-value stores were ruled out, because the queries need indexes across tables and ADK's session store only works through GORM |
| Model | Gemini through `model/gemini` with the owner's key; model name per toolset in settings |
| Sandboxes | Docker or OrbStack containers when present; workspace limits otherwise |
| Service API | `net/http`, server-sent events, OpenAPI with generated Go server and Dart client |
| Mail and calendar | Gmail and Google Calendar APIs, Microsoft Graph, IMAP/SMTP, CalDAV |
| Client app | Flutter for iOS, Android, macOS and Windows |
| Remote access | any private network, such as Tailscale; or public mode behind an OIDC access proxy whose signed identity the server verifies |
| Push | ntfy |
| Secrets | macOS Keychain, or a protected key file on Linux |
| Packaging | Homebrew formula, Docker image, release binaries built with GoReleaser |
| Tests | `go test` for the service; `flutter test` for the app |

### Not installed yet on this Mac
- Full Xcode, which iOS and macOS builds need.
- The Android SDK, which Android builds need.
- A container runtime, such as Docker Desktop or OrbStack.

## 13. Layout

```
heyrobots/
  service/                     Go module
    cmd/heyrobotsd/            server, setup page, launchd and systemd install
    cmd/adkdev/                development only: loads the router and Executor agents into ADK's own web UI to debug prompts and tool calls
    internal/
      core/                    Trigger, Thread, Ticket, Mandate, Assignment, Decision, Effect, Reach
      assistant/               the owner's Assistant
      devices/                 pairing, device and delegate tokens
      store/                   GORM models, transactions, migrations, backups
      gate/                    deterministic admission, authenticity, toolset check
      policy/                  owner identities, delegates, rules, standing approvals, limits, versions
      intake/                  polling, webhooks, dedup, Subscription fan-out, batching
      router/                  ADK router agent and routing tools
      executor/                ADK executor runtime, report, ack, hand-back
      sandbox/                 containers, workspace limits, network proxy
      outbox/                  senders, last checks, approvals, send caps, ordering, retries, crash recovery, digest
      usage/                   model token use, prices, monthly budget
      vault/                   encrypted credentials and OAuth
      api/                     HTTPS, SSE
      notify/                  ntfy
      privacy/                 export, wipe, retention
      google/                  OAuth with the owner's own client, Gmail and Calendar API calls
      sources/                 app, schedule, mail, calendar, slack, github
      toolsets/                research, writing, calendar, mail, data, code
    prompts/                   router.md, executor.md, toolsets/*.md
    e2e/
  api/openapi.yaml
  app/                         Flutter companion app
  docs/setup/                  setup guides for each provider
```

How the layout uses ADK:
- **ADK stays at the edges.** Only `router/`, `executor/` and `toolsets/` import ADK packages. `core`, `gate` and `policy` import none, so they stay pure Go with plain tests, and the deterministic gate can't drift into model code.
- **ADK is a library here, not the host.** Agents are built with `llmagent.New`, tools with `functiontool.New`, and each attempt runs through `runner.New` with the database session store. Our own code owns intake, the gate and the outbox.
- **Approvals live in the outbox.** ADK has its own tool-confirmation feature that pauses a tool for human approval. It isn't used, because outward tools here only queue an Effect. Approval belongs in the outbox, where it is durable across restarts and follows the reach rules.

## 14. Path to a hosted service

A hosted service stays possible because the self-hosted design already keeps the boundaries it needs:
- Every row and file belongs to one Assistant, and every API request is scoped by its token. A hosted server can therefore hold many Assistants, each with its own database file, without changing the domain code.
- Sandboxing, send caps and budgets already exist.
- The companion app already connects to whatever server it is paired with.

Hosting adds work that self-hosting avoids, and it comes only after the self-hosted release has users:
- public sign-in with passkeys, Apple and Google;
- stronger isolation with gVisor or Firecracker sandboxes;
- Google app verification, plus the yearly security assessment for Gmail access;
- native push through Apple and Google;
- billing, a privacy policy, terms and processes for data protection law;
- abuse controls for open sign-up.

## 15. Build order

Each step ends with a checkpoint for review.

1. **Core and gate.** Domain types, reach, Policy, and the gate as a pure function. Tests cover owner, delegate, forged email, anyone-else, the toolset check at accept time, and a delegate token being refused as an approver.
2. **Server, setup and pairing.** `heyrobotsd`, the data folder, migrations, the setup page with its terminal code, device pairing, the API, server-sent events, and the public-mode guard check. Tests prove that a request through a local tunnel can't reach setup, and that public mode fails closed without a valid proxy identity.
3. **App Source and Eval.** Chat through the API, dedup, Threads, batching and the router.
4. **Executors and sandbox.** The research and writing toolsets, containers with the network proxy, the fallback workspace limits, reach checks, quarantined reading, and the taint rule's check on controlling details. Tests include an email that names a new recipient and a link to an untrusted domain, both of which must need fresh approval.
5. **Outbox and recovery.** Approvals, send caps, digest, budget stops and restart recovery.
6. **Flutter companion app.** Phone and desktop apps, QR pairing, biometric approvals and ntfy pushes.
7. **Mail, calendar and schedules.** Setup guides, Gmail and Google Calendar, the Assistant's plus-address with DKIM checks, the schedule Source, and starter rules in observe mode.
8. **Privacy controls and packaging.** Export, wipe, retention, Homebrew, Docker image, release binaries and the remote-access guides.
9. **GitHub and code.** The GitHub App and the code toolset. This is the original brief.
10. **Microsoft 365, IMAP and CalDAV.**
11. **Slack.**
12. **End-to-end tests.** These run against fake provider servers. They cover a request from the phone, a forged email being refused, an approval with biometrics, a proactive rule in observe and then auto mode, a delegate limited to their toolsets, and a wipe.
