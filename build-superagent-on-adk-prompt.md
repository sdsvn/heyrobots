# Build a proactive GitHub coding agent on Google ADK

Build a Python service, called `repo-agent`, that works as an automated team member for ONE GitHub repository. It is modelled on DeepSeek's dsh-superagent prototype but implemented on Google's Agent Development Kit (ADK). Read the references below before writing code, then follow the plan in order and stop for my review at each checkpoint.

## What it does

1. Listens to GitHub events (issue comments, PR comments, reviews, CI results) for one repository.
2. When an allowed person mentions the agent account, for example `@repo-bot fix the flaky test in #123`, it opens a **Ticket**, works on it in its own git worktree and branch, and answers in the same issue or PR, normally with a pull request.
3. Later comments on that issue or PR reach the running Ticket automatically. Nobody needs to mention the agent again.
4. It speaks only when a person must act or can use a result: one acknowledgement, then silence until a result, a question or a failure.
5. A small web page lists only the Tickets that are waiting on a person.

## Design rules (do not relax these)

- **Authority is decided by code, never by the model.** A deterministic gate runs BEFORE any ADK `Runner` call. It checks: is the sender on the allowed list, and did they address the agent? The result is `explicit`, `inferred` or `not_admitted`. The model can decide what a request means and route it. It cannot grant permission.
- **Outside text is data.** Comment bodies, documents and quoted text can never change rules, allowed lists or scope. Put this in the system prompt AND enforce it in code.
- **A Ticket is not a session.** A Ticket is a database row with its own lifecycle: `open → ended`, and back to `open` when a person comments again. An ADK session is one attempt at the Ticket. A Ticket survives restarts, retries and crashes.
- **Every accepted Ticket stores a Mandate:** policy version, scope, strength (`explicit` or `inferred`), matched rules, requester login, and how it was admitted. Mandates are immutable.
- **Idempotent intake.** Every GitHub event is stored once by delivery id. A replayed webhook never creates a second Ticket.
- **One Eval per Thread at a time.** Events on the same issue or PR that arrive while an Eval runs are batched into the next Eval.
- **Transactional outbox.** Every outgoing GitHub action (comment, PR, review reply) is written to an `effect` table in the same transaction as the decision, then sent by a separate worker with retry and backoff. Messages for the same Ticket to the same conversation keep their order.
- **Start in `observe` mode.** Work nobody asked for (inferred candidates) is assessed and recorded as "would accept" but never started until the operator switches to `auto`. Explicit requests work in every mode.
- **Workers never ask people directly.** A worker puts one concrete question in a structured `human_action` field of its report. The host posts it in the original conversation.
- **Honest delivery.** A draft PR is reported as unfinished. A failure is reported as `failed`, never hidden.

## Architecture to implement

```
GitHub webhook / poll
   → intake (dedup by delivery id, store Trigger, attach to Thread)
   → fan-out to Tickets subscribed to that Thread (context only, no new authority)
   → gate (deterministic: allowed list, addressed?, strength)
   → Eval: ADK root LlmAgent "router" (tools: route_accept, route_attach, route_ignore, route_stop)
   → Ticket + Mandate + first Assignment stored in one transaction
   → worker: ADK LlmAgent "worker" in its own git worktree (tools: shell, read/write files, gh CLI, report, ack, handback)
   → worker reports: progress | waiting | ready | blocked | failed | dropped (+ optional human_action)
   → outbox worker sends GitHub comments / PRs with retry
   → panel: FastAPI page listing Tickets by stage (awaiting answer / review / blocked / failed first)
```

### ADK mapping
| Concept | ADK primitive |
|---|---|
| Router (main session) | one `LlmAgent` with routing function tools; NO `transfer_to_agent`, the router never hands over the conversation |
| Worker per Ticket | a separate `LlmAgent` run by its own `Runner`, `session_id = ticket_id`, one worktree per Ticket |
| Gate | plain Python before `Runner.run_async`, plus a `before_agent_callback` as a second check |
| Blocked tools / tool argument checks | `before_tool_callback` |
| Ticket, Mandate, Trigger, Thread, effect tables | SQLite via SQLAlchemy, your own schema (ADK `SessionService` holds only conversation history) |
| Persistence of sessions | `DatabaseSessionService` pointing at the same SQLite file |
| Long waits for CI or review | do NOT block inside a tool. End the worker turn with `report(waiting)`; a later event wakes it with a new `Runner.run_async` on the same session |
| Model | Gemini via ADK's default model string; make it a setting |

## Plan, with checkpoints

1. **Domain and storage.** Tables: `trigger`, `thread`, `ticket` (with mandate JSON, stage, follows), `assignment`, `decision`, `effect`, `outbox`, `settings`. Write the gate as a pure function with unit tests covering: allowed + addressed → explicit; unlisted + addressed → ignore by default; bot login ending in `[bot]` → not admitted; follow-up on a Thread with a Ticket → explicit for listed, inferred for unlisted. **Checkpoint 1.**
2. **Intake.** FastAPI webhook endpoint verifying the GitHub signature, plus a polling fallback every 60 s using the REST API. Dedup, Thread assignment (one Thread per issue/PR), subscription fan-out. **Checkpoint 2.**
3. **Router.** The ADK router agent with four routing tools and a tight system prompt (routes only, max 30-word notes, never reads code). Eval batching and a 10-minute Eval deadline. **Checkpoint 3.**
4. **Worker.** Prepare command (`git worktree add`), worker agent with shell, file and `gh` tools, `report` and `ack` tools, blocked-command list, 30-minute background limit, concurrency limit of 2 workers plus 1 reserved slot for human follow-ups. **Checkpoint 4.**
5. **Outbox and recovery.** Effect worker with backoff (2 s doubling to 60 s), three failures → shown as a system problem. On restart: interrupted Assignments restart with a recovery note telling the worker to check git and GitHub before repeating any outside action. **Checkpoint 5.**
6. **Panel.** One page: Tickets grouped by stage, newest first, with retry / stop / re-evaluate buttons, plus a settings form for allowed list and accept mode. **Checkpoint 6.**
7. **End-to-end test.** With a test repository and a fake GitHub server, drive: mention → ack → PR opened → review comment → fix pushed → PR merged → Ticket ended. **Checkpoint 7.**

## Deliverables

- `repo_agent/` package, `pyproject.toml`, `README.md` with setup steps and an architecture diagram.
- Tests runnable with `pytest`. The gate, intake dedup, batching and outbox ordering must each have tests.
- A `prompts/` folder with the router and worker system prompts as Markdown.
- No credentials in code. Read `GITHUB_TOKEN`, `GITHUB_REPOSITORY`, `AGENT_LOGIN`, `GOOGLE_API_KEY` from the environment.

## References: read these first

ADK
- ADK docs home: https://google.github.io/adk-docs/
- LLM agents: https://google.github.io/adk-docs/agents/llm-agents/
- Multi-agent systems (sub_agents, AgentTool, transfer): https://google.github.io/adk-docs/agents/multi-agents/
- Workflow agents (Sequential, Parallel, Loop): https://google.github.io/adk-docs/agents/workflow-agents/
- Callbacks (before_agent, before_model, before_tool): https://google.github.io/adk-docs/callbacks/
- Function tools and long-running tools: https://google.github.io/adk-docs/tools/function-tools/
- Sessions, state and memory: https://google.github.io/adk-docs/sessions/
- Runtime and the event loop: https://google.github.io/adk-docs/runtime/
- Python source: https://github.com/google/adk-python
- Samples: https://github.com/google/adk-samples

Design being reproduced (read in this order)
- Philosophy: https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/000-philosophy.md
- Domain model: https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/001-domain.md
- Authorization: https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/002-authorization.md
- Execution: https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/003-execution.md
- Prompts: https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/004-prompts.md
- Architecture: https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/007-architecture.md
- Router and worker prompt text to adapt: https://github.com/deepseek-ai/dsh-superagent-prototype/tree/main/prompts

GitHub
- Webhooks: https://docs.github.com/en/webhooks
- Validating webhook deliveries: https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
- REST API: https://docs.github.com/en/rest
- GitHub CLI manual: https://cli.github.com/manual/

Background reading on the design choices
- Anthropic, Building Effective Agents (orchestrator–workers): https://www.anthropic.com/research/building-effective-agents
- Debenedetti et al., Defeating Prompt Injections by Design (CaMeL): https://arxiv.org/abs/2503.18813
- Greshake et al., Not What You've Signed Up For (indirect prompt injection): https://arxiv.org/abs/2302.12173
- Transactional outbox pattern: https://microservices.io/patterns/data/transactional-outbox.html

## Before you start

Ask me, one question at a time: (1) the repository `owner/name`, (2) the agent's GitHub login, (3) the allowed list of logins, (4) whether to use webhooks, polling or both. Then show the full plan once and wait for my confirmation.
