---
co-review: design
---
# Give every agent a durable identity and file history

## Context

An agent currently persists its name, purpose, instructions, tools and model in the database. Its task workspace is created per ticket (or per chat), and the database already holds the authoritative conversation and Assignment history. Creating an agent does not create a durable folder or a human-readable identity file, so an agent has no stable file context spanning tasks.

## Design

- Create one local agent directory at `workspaces/agents/<agent-id>/` when an agent is created or first synchronized on its home machine.
  - Write `AGENT.md` from the saved name, purpose, specialist role, allowed toolsets, skills and owner instructions.
  - Treat the database as authoritative; this file is a readable materialized profile, not an instruction file that can silently change runtime permissions.
  - Regenerate only the managed profile section on agent updates; preserve conversation, session and user note files.
- Materialize the agent's participation history below the same directory.
  - Write `conversations/<thread-id>.md` as a deterministic view of the thread's messages, agent replies and relevant timestamps.
  - Write `sessions/<assignment-id>.md` for each Assignment with ticket, thread, scope, start/end, recovery state, report state, summary and result.
  - Synchronize after an Assignment starts/ends and whenever a participating thread changes; use temp-file-plus-rename writes so a crash leaves the previous complete file.
  - Keep the database as the source of truth; these files are recoverable views and must not be used to bypass policy or restore deleted history.
- Make the agent profile available to every worker run.
  - Include the managed profile in the existing agent prompt and copy it into the task workspace for ACP workers.
  - Keep task workspaces unchanged so existing files and ACP session continuity remain compatible.
- Wire the API and daemon to the same configured workspaces root.
  - Agent creation must fail rather than report success if its profile directory cannot be created.
  - Remote agent cards create files only on the machine that owns the agent; cluster synchronization must make the profile when that row arrives home.
- Preserve privacy and lifecycle rules.
  - Do not write API keys, credentials or policy secrets to files.
  - Clear-history removes conversation and session files with chat history; deleting an agent removes its profile directory only after its open-task check passes.

## Alternatives

- Keep only database history: avoids duplication but does not give the agent or owner a durable, inspectable file context.
- Use one folder per task only: already exists, but loses identity and continuity across conversations.
- Let the model freely author `AGENT.md`: rejected; inferred wording can be generated, but permissions and runtime instructions must remain controlled by stored configuration.

## Decisions

- Responsibility and role wording are derived deterministically from the saved purpose and specialist metadata. No extra model call runs during agent creation; this avoids cost, latency and hallucinated permissions.
- Conversation files contain full message bodies because they are a local materialized view of history and the agent already has access to that conversation. The database remains authoritative.
