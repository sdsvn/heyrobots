---
co-review: design
---
# Let everyone in a group hear every message, one member claim the work, and no message wait behind another

## Context
In a group chat today, the router (one quick model call) reads each message and picks which member answers. That one call is behind every group bug so far: it answered in its own voice, gave a member tools it couldn't use, and sent "both of you" to one member. Each answer is also a fresh task with its own session, so a member forgets what was said in the group a minute ago unless it is in the same task.

The change: in a group, every member hears every message. Each one decides for itself whether to answer, and real work is claimed by one member so it is never done twice. Each member keeps one running conversation per group, kept small, so it remembers the group without carrying everything else it knows.

## Design
- A group message reaches every member, with no router
  - the eval engine skips the router for group chats: each message is delivered to each member's group conversation
  - an @mention still reaches only that member
  - the router stays for everything that isn't a group: the main Bit chat, mail, other people's messages
  - what can go wrong: a big group means many turns for every message; groups of more than 4 members keep the router, and the group screen says so
- Each member first decides, cheaply, whether to answer
  - the decision sees only the last few messages, the member's purpose and who has claimed what, not the whole conversation
  - it runs on the quick-calls model; with your own agent (omp or Claude Code) as the quick model, all members' decisions for one message go in one call
  - the answer is one of: stay quiet, answer, or claim the work
  - staying quiet shows nothing, so "thanks" gets no replies
  - what can go wrong: every member stays quiet on a real request
    - when nobody answers or claims in the decision round, the member whose purpose fits best answers, so a message never goes unanswered
    - when none fits, the first member in the group's order answers
- A message for everyone gets an answer from everyone
  - "introduce yourselves", "both of you, what can you do?" or "what does each of you think?" asks each member: every member answers, and nobody claims
  - the decision step reads this from the message, the same way it reads an @mention
- Work is claimed by one member
  - only work that should happen once is claimed: a search, a draft, a booking; a question to everyone is not
  - a member that will do real work (search, write, send, change something) claims the message first; the claim is a short entry in the group ("Coder is on it")
  - claims are settled by order: the first claim wins, the others see it and stay out unless you ask them in
  - this reuses the rule the machines already use to settle who routes a message
  - what can go wrong: two members claim at the same moment; the store takes them in one transaction, so exactly one is first and the other is told it lost
- Members coordinate in the group, in the open
  - a member can post to the group and @mention another member, to split work ("Researcher, find the hotels; I'll do flights"), ask for a piece, or hand over a result
  - a mentioned member hears it like a message from you and answers or does that piece, under its own claim
  - you see every such message in the chat, so coordination is never hidden
  - what can go wrong: two members talk in a loop; after 6 member messages in a row without you, the group pauses and waits for you
  - what can go wrong: one member asks another to send or change something outward; that still waits for your OK, as it does today, and a member can never approve for you
- A new message never waits behind a task that's already running, in any chat
  - today a message in a chat with a running task goes to that task, waits for its current step, and only then is handed back if it asks for something else
  - instead, when it arrives, a quick check reads it with the running task's scope and says: about that task, or new
  - about the task: it goes to the task as now, and is read at its next step
  - new: it starts at once as a new task on a free slot of the same agent (one slot is always kept for your messages), with the chat's recent messages as context
  - this holds in a direct chat and in a group alike
  - what can go wrong: the check guesses wrong
    - "stop" and "cancel" always go to the running task, never to a new one
    - a task that gets a message it can't use still hands it back, as today, so a wrong guess costs a step, not the message
- Each member keeps one conversation per group
  - a new kind of session, keyed by agent and group, holds the group's messages as they arrive, the other members' replies included
  - it only ever grows, so the provider's cached prefix stays valid from one turn to the next
  - when it gets long, older turns are folded into a short summary at the top, and the latest turns stay word for word; the fold happens rarely, at a fixed size, so caching is lost only then
  - what can go wrong: a member was added to the group later; it starts from the summary and the latest turns, not the whole history
- A member's context has three layers, and only the middle one grows
  - who it is: purpose, your instructions, its skills list and tools, fixed and cached, exactly as today
  - this group: the group conversation above
  - this piece of work: a claimed task keeps its own task session, files and tools, as today; the group conversation only gets "I'm on the flight search", and later the result
  - what it knows from other chats stays out; it looks things up when needed, through its memory notes or the team tool
- Your own agent keeps one session open per group
  - for omp and Claude Code, the member's ACP session for the group stays open between messages, instead of a new process per task
  - it stops re-reading its instructions on every message, which also makes answers faster
  - what can go wrong: an idle session holds memory; sessions close after 30 minutes of quiet and reopen from the saved conversation
- The app shows who is answering
  - while members decide, the thinking bubble shows each member's face
  - a claim shows as a small line in the chat, like "Coder is on it"; quiet members show nothing

## Alternatives
- Keep the router for groups and keep fixing its mistakes: one call per message, but every fix so far has been a patch around a model guessing for the members.
- Send every message to every member with no claims: simplest, but two members would both book the same flight. Claims cost one short entry and stop that.
- One shared conversation for the whole group instead of one per member: smaller, but every member would then see the others' tool calls and private notes, and a member's own instructions couldn't stay at the cached top.

## Open Questions
- Is 4 members the right limit before a group falls back to the router?
- Should quiet members show anything, such as a faded "read" mark under the message, or stay fully hidden?
