---
co-review: design
---
# Give each agent its own standing jobs, and talk to agents and groups like chats

## Context

The approved design (agents-tools-transport) gave us a standing team of agents, each with its own tools, skills, model, memory and queue. Three things changed since. heyrobots moved to ADK Go v2.7.1, whose graph workflow engine can run deterministic multi-step jobs with retries, and can pause for a person and resume after a restart. You asked that agents work on their own, not only in chats: a schedule or a trigger should belong to one agent and run a job with known steps. And you reviewed mockups where the app has three tabs, Chats, Team and Plans, with direct chats with an agent and groups of agents.

Today a schedule speaks as the person who made it, and a rule is text the router reads; both belong to the assistant as a whole, so work from them goes to whichever agent the router picks, and the agent improvises the steps every time. A conversation belongs to a Source and a thread key, never to an agent, so there is no way to address one agent or a set of them.

## Design

- Each agent can own standing jobs: a job runs when its schedule fires or its trigger matches, with steps the owner can see (new table `jobs`, owner-only endpoints, the agent's profile in the app)
  - A job has an owner agent, a name, what starts it, the toolsets it may use, and a plan: an ordered list of steps
    - What starts it is a schedule, or a trigger: a label in an MCP server, a failed CI run, mail that matches a filter
  - Each step is plain code (read the issue, run the tests, post to the channel), a model step with a short instruction, or another job; the agent's instructions and skills apply to model steps
    - Plain steps run without a model call, so a job that only moves a ticket or sends a report costs nothing to think about
    - A job used as a step runs as a sub-workflow inside the caller, under the caller's task and toolsets; a job can't call itself, directly or through another job, and that is refused at save time
  - Jobs are defined in a jobs folder in the data folder, one file per job in the format of a skill's frontmatter; the app shows them and their runs but doesn't edit them yet
  - The job's toolsets must be ones the agent may use; what a run may reach is the job's toolsets, the same way a task's Mandate caps it
  - A job needs a matching enabled rule, exactly as proactive work does today, so accept mode and the rules still decide whether anything runs; the rule names the agent it may use
  - Starting a job makes a task like any other: it has a Mandate, waits in the agent's queue, and its outward calls go through the outbox
- A job runs as an ADK workflow (new package `internal/jobs`, built on ADK v2's `workflow` package)
  - Each step becomes a node: a function node for plain steps, an agent node for model steps; a step can be marked repeatable, which wraps it in a retry with backoff and a timeout
  - A step that needs the owner (your OK before a PR, an answer to a question) pauses the workflow with ADK's request-input event; the run state is written to the session, and the answer resumes it, after a restart too
    - The pause shows in the app as a question or approval on the job's task, through the same attention feed as today
  - A step's outward call still never runs inside the workflow: it becomes an outbox action, and the step waits for that action's outcome before the next step
  - What can go wrong: a job whose plan can't be built (an unknown step, a toolset the agent lacks) fails at save time, not at run time
- The schedule Source and rule triggers now start jobs, and today's schedules keep working
  - A schedule names a job; the old schedules, which carry a note, keep speaking as the person who made them and go through the router as now, so nothing existing breaks
  - Triggers from MCP servers (an issue labelled, a CI failure) come from a watcher that polls the server's read tools on an interval you set
    - It starts the job's task when a new matching item appears, and remembers what it has seen
    - Polling is opt-in per job: nothing is polled unless a job's trigger names the tool, and only tools you marked as reads; the watcher never writes
- You can chat with one agent, or with a group of agents, the way you chat with people (new table `groups`, owner-only endpoints, the Chats tab)
  - A direct chat is a conversation whose thread belongs to one agent: the router still decides what the message means, but the lead agent is fixed
    - If the message needs a toolset that agent lacks, the router refuses it with the reason, instead of silently picking another agent
  - A group is a named set of agents; a group's conversation gives the router only the group's members to choose the lead from
    - An @mention of a member in your message fixes the lead to that member for that message's task
    - Members hand work to each other inside the group's task with the message tools from the approved design; the side-talk shows in the group chat with each member's avatar
  - A delegate may use a direct chat or a group only when every agent in it is one their requests may go to
  - Conversations from mail, calendar and schedules stay as they are, led by whoever the router picks from the whole team
- The app gets three tabs: Chats, Team and Plans, per the reviewed mockups
  - Chats lists direct chats, groups and the other conversations, with the last message, the time and an unread count
    - A chat shows "typing…" while an agent works on it, and a strip on top says how many agents are working outside any chat
  - Team shows each agent's reach, what it is doing, what comes next, and its jobs
    - An agent's profile has its running task with steps, its standing jobs, what it can reach, its recent activity, and the buttons Message, Give it a job, and Pause
  - Plans shows, across the team, what needs you, what runs now, and what comes up today or this week, each item saying where it came from: a chat, a trigger or a schedule
  - The steps shown for a running job are the workflow's nodes and their states, read from the job's run state
  - Today's home screen and its waiting list become the Chats tab's strip and the Plans tab's "needs you" section
- Agents' prompts are built so providers can cache them (changes to the executor and the message tools from the approved design)
  - An agent's session on a task only grows: a teammate's message is appended as a new turn, and nothing earlier is rewritten
  - What a teammate is doing comes from a tool call when the agent asks, never from the system prompt, so the cached prefix stays identical between calls
  - The system prompt holds only what is fixed for the task: the base prompt, the agent's identity and the task section; memory and notes from the host go in the user turn, as they do now

## Alternatives

- Keep jobs as rules plus schedules, and let the agent improvise the steps: simplest, but every run spends model calls on steps that are the same every time, and nothing can wait for you across a restart without the whole task ending.
- Use ADK's workflow engine for the whole pipeline (intake, routing, queues, outbox): one execution model, less code of ours, and retries and pauses for free. But it would replace stores built for days-long work (an outbox that never sends twice, leases, a budget stop, an immutable Mandate) with run state kept in a session's history; it has no queues or capacity, so a busy agent couldn't make work wait; and an ACP agent, a separate process, can't be a node in its graph. The engine pays off inside a job, where steps, retries and pauses are the point.
- Groups as ADK sub-agents under a coordinator: they would run inside one call, so a busy member couldn't make work wait, and an ACP agent couldn't be a member.

## Behaviour

- **The server restarts while a job waits for you.** The task's Assignment lease expires; recovery reconstructs the workflow's run state from the session and the job stays paused at the same step until you answer.
- **A step fails repeatedly.** After its retries the workflow fails, the task reports why, and the job shows the failed step in its profile; the next scheduled run starts fresh.
- **A trigger fires while the agent is at capacity.** The task waits in the agent's queue; a trigger that fires again for the same item starts nothing, because the watcher remembers it.
- **A group member is removed from the team.** Open tasks it leads keep running until they end; the group drops it, and the router picks from the members left.

