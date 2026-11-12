---
co-review: design
---
# Let tasks use MCP, skills, other models and subscription agents, and stream them live

## Context

Today every task runs in the built-in ADK worker on Gemini, with a fixed set of toolsets (research, writing, mail, calendar). A rule or request grants toolsets in the task's Mandate. Calls to outward tools (sending mail, inviting people) are caught by name and become actions in the outbox that may need your approval. Outside content (web pages, mail) is read in quarantine. The app talks to the server over REST, and one event stream only says "this thread changed", after which the app reloads it.

You want tasks to use your MCP servers and skills, to run on other model providers, and to run in Claude Code, Codex or Gemini CLI under your own subscription. You also want the conversation to show Bit working live. All of this has to keep today's rule: nothing reaches other people without passing the outbox checks.

## Design

- Every tool a task can use goes through one tool gateway (new package `internal/gateway`), whichever worker runs the task
  - It holds the tools the task's Mandate grants: the built-in toolsets, the MCP servers you added, and the control tools (report, ack, hand_back)
  - It serves them two ways: as ADK tools for the built-in worker, and as an MCP endpoint for agents that run outside the server
    - The MCP endpoint is per Assignment, with a token that only works for that task's granted tools and expires when the Assignment's lease ends
    - A leaked token reaches only that task's tools, for minutes, and outward calls still wait in the outbox
  - Every call takes the path the executor uses today: an outward call becomes an action in the outbox with reach, taint and approval checks, and outside content goes through quarantine
  - The executor's outward-tool check and its proposal step move into the gateway unchanged, so the built-in worker behaves exactly as it does now

- You can add MCP servers, and each one becomes a toolset a rule can grant (new table `mcp_servers`, owner-only endpoints, a Tools screen in the app)
  - A server is a command the server starts (stdio) or an https address; its tokens live in the vault and never reach a task
  - Each tool on a server is one of: read, outward (with the reach you pick: you only, other people, or can't be undone), or blocked
    - A tool's own read-only hint can't be trusted, so a tool you haven't reviewed is off: agents don't see it, and the Tools screen shows a "new tools to review" badge
    - Tool descriptions come from the server and go into the model's prompt, so they are capped in length, shown to you when you review the tool, and a changed description turns the tool off again until you review it
  - Results from read tools go through quarantine: the gateway adds a "question" argument, and the worker gets the quarantined answer, as with web_fetch
    - You can mark a server you control (say, your own Linear) as trusted; its results then reach the worker directly, still marked as outside content for the taint check
  - An approved outward MCP call is run by a new outbox sender that calls the tool with the stored arguments
    - MCP gives no idempotency, so a timeout marks the action "may or may not have happened", as with mail today
  - The server is called `mcp:<name>`; an agent gets it only when its profile lists it
  - A stdio server runs on your machine with your trust, like in any MCP client; heyrobots starts it, gives it only its own tokens, and stops it when idle

- You have a standing team of agents that share the work: each is one persistent teammate with its own tools, MCP servers, skills and worker, not a copy started per task (new table `agents`, owner-only endpoints, an Agents screen in the app)
  - An agent has a name and purpose ("Researcher", "Coder", "Inbox helper"), its instructions, its toolsets and MCP servers, its skills, and its worker: the built-in worker with a chosen model, or a registered ACP agent
  - Work is handed to agents, not agents to work: each agent has one queue and works on one task at a time by default, with a capacity you can raise
    - Each Assignment now names the agent doing it; an agent picks the next one from its queue when it's free, so a busy agent means waiting, never a second copy
    - An ACP agent's capacity stays at one, since subscriptions have rate limits
  - The router picks the lead agent for a task, the way it picks toolsets today: only from the agents the request's sender or matching rule may use, which code works out first
    - Your own requests may use any agent; a delegate's request or a proactive rule may use only the agents that rule names
    - The router sees each agent's name, purpose and how busy it is, never its tools or instructions, so it can't widen what a task can do
  - Agents talk to each other and to the task's root through messages on the task, in the first version (gateway tools to message a teammate or the root)
    - The root is the task's lead agent; above it sits the router, which still alone decides whether work is accepted, stopped or routed elsewhere
    - An agent can ask a teammate for a piece of work, ask it a question, or send it a result; the message goes into the teammate's queue as input on the same task
    - An agent can tell the root it is blocked, done with its part, or that the task needs a teammate it can't reach; the root decides what happens next, and only you can widen what the task may do
    - Sending a message never waits: the sender's turn ends "waiting" and resumes when a reply arrives, like a follow-up today, so no agent ever blocks on another
    - Messages stay on the task: they're stored with it, shown in the conversation as the team's side-talk, and traced, so you can see every exchange
    - A task has a limit on messages between agents, so they can't pass work back and forth forever
    - Every outward call still goes to the same outbox under the task, and all spending counts toward the task
  - What an agent may do on a task is what both its own setup and the task's Mandate allow, so joining a task never adds powers the task wasn't given
    - The Mandate fixes which agents may work on the task when it is accepted; editing an agent changes what it can reach on its next Assignment, but never beyond the Mandate
  - An agent keeps its own memory between tasks, kept apart per person it works for
    - What it learned on your tasks is used on your tasks only; a delegate's tasks get memory of that delegate's tasks only, so nothing leaks between people
  - Every agent is easy to see in the app
    - Each agent has its own color and a Bit wearing its own small badge, so you can tell at a glance who is working
    - The Agents screen lists the team as cards: what each is for, its worker and model, what it can reach, and what it's doing now ("working on Trip to Lisbon", "2 waiting", "idle")
    - Conversations, waiting items and the digest show which agents are on a task; each agent's avatar sits beside its messages
    - Hand-offs show in the live stream as steps ("Coder asked Researcher to check flights"), using AG-UI's sub-agent events, with the teammate's tool calls under it
    - What an agent can reach is shown as plain chips ("Sends email · needs your approval"), so the card doubles as a permission summary
  - Today's setup becomes a team of one, a built-in "General" agent with today's toolsets, so existing rules keep working

- You can keep a library of skills that tasks can use (a skills folder in the data directory, owner-only endpoints, a Skills section in the app)
  - A skill is a folder with SKILL.md in the format Claude Code uses; you add one by putting it in the folder or uploading it from the app, never from a task
  - A task sees only the skills its agent's profile lists
  - The built-in worker loads them with ADK's skill toolset: names and descriptions in the prompt, the full text when the worker opens one
  - Scripts in a skill run only in the container sandbox; without Docker or OrbStack, skills with scripts are unavailable
  - A skill only gives instructions: it can't add tools, so what a task can do still comes from the Mandate

- Each role can use a different model provider (router, quarantine reader, built-in worker; new model adapters next to the current Gemini one)
  - Providers: Gemini (today), the Anthropic API, and any OpenAI-compatible endpoint (DeepSeek, OpenRouter, Ollama, LM Studio, vLLM)
  - A coding harness reached over ACP (Claude Code, Codex, Gemini CLI) can be an agent's worker, but not the router's or the quarantine reader's model
    - ACP runs a whole agent with its own tools and system prompt, not a single model call, so it can't give the router's strict structured decision, and starting a session per message is slow and eats subscription limits
    - The quarantine reader must be a model with no tools, so a page that says "run this command" has nothing to run; a harness always has a shell
    - So subscriptions power the agents that do the work, and the router and quarantine reader stay on a plain model API, which can be a cheap or local model
    - This needs two new `model.LLM` adapters, one for the Anthropic Messages API and one for OpenAI-compatible chat completions; ADK Go only ships Gemini
  - You pick provider, model and key per role in settings; keys live in the vault; a test call checks function calling and structured output before saving
  - Prices extend the existing price table; a local model costs nothing and its endpoint may be on your own network, which only you can set
  - Weaker models may handle tools badly; the router keeps its strict output checks, so a bad answer is refused, not acted on

- A task can run in an agent you already pay for, over ACP (new worker backend in the executor, using an ACP client library)
  - You register an agent once, with the command to start it; you install it and sign in yourself
    - The first version supports Claude Code, Codex and Gemini CLI together, and any other agent that speaks ACP through the same generic path
    - Each needs a small adapter for its quirks: where it reads skills, where its login lives, which API hosts it calls
    - DeepSeek runs either as an API provider for the built-in worker, or as the model behind an ACP agent that can be pointed at DeepSeek's API
  - An agent profile can name a registered ACP agent as its worker; the Mandate's snapshot fixes it for the task
    - The router stays on an API model, because it runs on every message
    - The agent never asks you anything directly: its permission requests are answered by heyrobots in code, and its questions reach you only through the report tool's one question
  - The agent runs only in the container sandbox; without a container runtime, agent workers are off
    - It sees the task's folder and nothing else; its network goes through the egress proxy, allowed only to its vendor's API hosts and the gateway
    - It runs with its own settings folder inside the container, not your personal one, so your hooks, MCP servers and permissions don't apply
    - Its login is mounted read-only (you've accepted this); anything it runs could read it, but the network allowlist only reaches the vendor and the gateway
  - heyrobots is the ACP client: the session's working folder is the task folder, and its only MCP server is the gateway
    - Allowed skills are copied read-only into the folder the agent reads (for Claude Code, `.claude/skills`)
    - Permission requests are allowed for edits and commands inside the container, and refused for anything else
    - The agent's own web tools hit the proxy's allowlist, so web reading goes through the gateway's quarantined tools
  - The task ends when the agent calls the gateway's report tool; if the session ends without one, the host writes the failure report as it does today
  - Stopping a task cancels the ACP session and removes the container
  - Spending can't be measured, so these tasks have limits on time and turns instead of dollars, and the app shows "on your subscription"

- The app keeps REST and its event stream, and gains a live stream per conversation in AG-UI's event shapes (new endpoint for a thread's live events)
  - Events: a run started and finished per Assignment, Bit's text as it's written, tool calls by name with short arguments, the task's stage, and a custom event when a question or approval appears
  - Built-in worker events come from the ADK runner; agent events come from ACP session updates; both map to the same events
  - The stream is live only: on connect it sends a snapshot of the conversation, and history still comes from the existing thread endpoint
  - Same access rules as the thread: device login, and delegates see only their own conversations
  - Outward actions' bodies aren't streamed; the app shows "proposed an email", and the approval screen shows the details
  - Approvals stay on their REST endpoint with the fingerprint flag, and answers stay ordinary messages; there's no AG-UI "start a run" endpoint
  - The app decodes the few event types itself, rather than taking the community Dart SDK; web keeps reloading as today

- Everything every agent does is traced with OpenTelemetry, using its GenAI conventions, so any standard tool can show it (new package `internal/telemetry`)
  - ADK Go already emits spans in these conventions for agent runs, model calls and tool calls; heyrobots turns them on and adds its own around them
    - One trace per task: intake, the router's decision, each Assignment with the agent's name, every gateway tool call, approval waits and outbox sends
    - A hand-off is a span link from the asking agent's Assignment to the teammate's, so the trace shows who asked whom
    - ACP agents run outside ADK, so their session updates (messages, tool calls, permission requests) become spans with the same names and attributes
  - Attributes follow the conventions (agent name, model, tool name, token counts, finish reason) plus ours: task, Mandate, requester, reach, approval outcome, queued or sent
  - Content is off by default: spans carry names, ids, counts, timings and outcomes, never message bodies, tool arguments or anything from the vault; you can turn content on for your own debugging
  - Export is your choice over OTLP, to anything that reads it (Jaeger, Grafana Tempo, Langfuse, Phoenix); with nothing set, nothing leaves the machine
  - The server also keeps a short local activity log built from the same spans, so the Agents screen can show what each agent did, hour by hour, without an outside tool
  - The live stream and the activity log come from the same source, so what you watch live and what you look back at always agree

- It ships in five releases, each one you can install and use on its own; every release adds its telemetry, so each is traceable from day one
  - Release 1, MCP tools: the gateway (no change for today's tasks), MCP servers with the Tools screen and tool review, and OpenTelemetry tracing with OTLP export
    - Try it: add an MCP server you use, review its tools, ask Bit for something that needs it, and see the trace in Jaeger or Langfuse
  - Release 2, the team: agents with their own tools, MCP servers and skills, the skills library, per-agent queues, and the Agents screen; one agent per task
    - Try it: set up a Researcher and an Inbox helper with different tools, and watch tasks go to the right one
  - Release 3, models: Anthropic, DeepSeek and OpenAI-compatible providers, chosen per agent and per role
    - Try it: run the router on a cheap or local model and an agent on Claude or DeepSeek, and compare cost in the app
  - Release 4, working together and live: messages between agents and to the root, the live stream in AG-UI's shapes, and the activity log
    - Try it: give a task that needs two agents, and watch them hand work back and forth in the conversation
  - Release 5, subscriptions: the ACP worker for Claude Code, Codex and Gemini CLI, in the container sandbox
    - Try it: make a Coder agent that runs on your Claude Code login and give it a small job in its folder
  - Each release keeps the earlier ones working; migrations only add tables and columns, so going back a release leaves the data readable

## Alternatives

- AG-UI as the whole API: its runs always start from the client, so it can't describe tasks that start from mail or schedules and run while the app is closed. It also has nothing for pairing, settings or approvals, and its pause fields are still draft.
- JSON-RPC between the app and the server: its real strength is that either side can call the other over one connection, which is why ACP and MCP use it. The server only ever needs to notify the app, which the event stream already does. We'd also lose what plain HTTP gives per path: status codes, safe retries, the auth guard and per-endpoint logs, and the spec that validates every request on the server. It would win only if the phone had to serve tools to agents (say, its location), which nothing here needs.
- Giving ACP agents your MCP servers directly: simpler, but every outward call would skip the outbox and your approval.
- Using a subscription's login as a model provider: generally not allowed by the vendors; ACP runs their own agent instead.
- Running ACP agents on your machine without a container: they have their own shell, and only the container and proxy keep them inside the task.

## Behaviour

- **The server restarts during an agent task.** The container is removed with the session; the Assignment's lease expires and recovery starts it again with a note, as today. The old gateway token has expired.
- **An MCP server is down.** Its tools return a failure the worker can report; if its tool list can't be read, the toolset is unavailable and the task reports itself blocked.
- **An MCP server changes its tools.** New tools arrive off until you review them; removed tools disappear from tasks at their next Assignment.
- **An agent ignores the gateway and tries the internet directly.** The proxy refuses it and logs the attempt.

## Open Questions

- Should a delegate's task ever run on your subscription, or only your own tasks?
