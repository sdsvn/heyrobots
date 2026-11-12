---
co-review: design
---
# Make several heyrobots servers act as one team

## Context
Today one heyrobotsd is the whole assistant: its agents, its lead agent (the router), its store and its phone pairing. The owner wants several of them, on a home Mac, an office box or a cloud machine, and wants them to feel like one team. Agents from every machine sit in the same chats. The app shows them all the same way; the only visible difference is whether an agent's machine is up. Every server keeps its own lead agent and still works alone. This replaces the cloud draft's split into a cloud cell and a Mac runner: a cloud server is one more member, not a different kind of thing.

The hard part is agreement. When two servers see the same message, which lead picks who answers? When the Mac is closed, who still knows what the chat said? A personal cluster is often two machines with one of them asleep, so anything that needs a majority stops working exactly when it matters. The design picks the weakest agreement that is still correct for each kind of decision. The work lives in a new package, `internal/cluster`, and a new table of chat events in each server's store.

## Design
- Servers join one cluster by pairing, the way a phone pairs today
  - the owner shows a code on one server and types it on another; both keep each other's public key and address
    - each server makes a key pair at first start; links between members use those keys both ways, never a shared password
    - the phone pairs once with any member and learns the others from the member list, so one pairing reaches every server
    - the phone is only a window onto the members; it runs no agents of its own
  - members only dial out: to each other directly when one is reachable (the LAN, a cloud server's public address), else through a relay that any always-on member offers
  - what can go wrong: a stolen server → the owner removes it from any other member
    - the removal spreads like any other change; a member that was off learns it on reconnect and is refused from then on
- The app shows one team, whatever machine an agent runs on
  - each agent has one home server, the only one that runs it and holds its memory, jobs, skills and tools
    - the other members keep a read-only card: name, avatar, what it's good at, home, last seen
  - the Team tab and chat member lists show every agent from every member; the only difference on a card is presence: available, busy, or unavailable since a time
  - presence comes from heartbeats: each member tells its links it's alive every few seconds
    - it also passes on what it heard about the others; clusters are small, so everyone simply hears everyone
    - a member silent for 15 seconds is unreachable, and its agents show unavailable; nothing else changes
  - what can go wrong: a network split shows each side the other side's agents as unavailable; each side keeps working with its own agents, and they merge when the link returns
- A chat is a log every member keeps a full copy of
  - each message, card, approval answer and task status is an event, written once by the member where it happened
    - it carries that member's id, a sequence number per member, and a hybrid logical clock: wall time plus a counter, so order survives clocks that drift
  - on every reconnect members swap what the other is missing ("I have up to 812 from you"); events are never changed after they're written, so every copy ends up the same
  - every member shows events in the same order: by clock, then by member id
  - the phone reads from whichever member it reaches first, and sees everything that member has
  - what can go wrong: an event arrives late and sorts above messages already shown → the app puts it in its place with an "arrived late" mark
    - a long chat's log grows on every member → old events fold into a summary, as agent sessions already do
- Exactly one lead decides who answers each message, without a vote
  - a message is routed by the reachable member whose copy of the chat is newest, since its lead sees the most context
    - heartbeats carry how far each member's copy of each chat has got, so every member can tell who that is; a tie goes to the lower member id
    - that member's lead posts a "routed to" event, as the router does today
    - an @mention needs no lead: the mentioned agent's home picks the message straight from the log
  - when that member doesn't claim the message within a few seconds, the next newest one routes it and says so in the log
    - if two members route the same message, every member keeps the claim that sorts first
    - an agent starts only after its claim has sat in the log for those few seconds, so the losing agent stands down before it acts
  - what can go wrong: during a split both sides route the same message → each side's agent answers on its own side
    - after the merge the chat shows both answers, the later one marked as a duplicate; outward actions can't double, because of the next step
- A task belongs to one server, so actions never need agreement
  - the agent's home runs the task and holds its lease, its Mandate check and its outbox, exactly as today; no other member can run that agent, even with its card
  - asking an agent on another machine is an assignment event in the log, and the answer comes back the same way: Release 4's agent-to-agent messages, carried across machines
  - an approval is signed by the owner's device and reaches the effect's home through any member
  - the owner's own memory notes live on one member the owner picks (the always-on one by default); the others read them over the link as a tool
  - what can go wrong: the home is off → the task waits with "this agent is unavailable", and the owner can hand it to an available agent with one tap
- Each server's lead can think with the owner's own ACP agent, so a machine needs no API key
  - when "Your agent" (omp, Claude Code or another ACP agent) is chosen for the quick calls, the lead routes by asking it in a throwaway session
    - it answers with the same routing calls as JSON, and they pass the same checks as tool calls
  - what can go wrong: the agent answers in prose → the message fails routing and is retried, as when a model call fails; reading outside content in quarantine still needs a model
- Settings that must match everywhere change rarely, so the owner decides them
  - the member list and the cluster-wide rules (spending caps, outward rules) are versioned documents
    - a change is signed by the owner's device, every member applies it on receipt, and the higher version wins
  - there is no quorum: the owner is the authority, and two members that disagree about a rule only means one hasn't heard yet

- Other people join a chat by invite, with their own agents
  - the owner invites a person to one chat; they accept on their own phone, and their servers start receiving that chat's log and nothing else
    - the invited person and their agents show in the chat like anyone else, with the same presence states
  - guest servers route only messages that @mention one of their agents; the owner's servers route everything else
  - a guest's agents act only through their own home's outbox and Mandate, under the guest's rules, never the owner's
  - what can go wrong: a guest's server sends events about someone else's agent → events are signed by their home server and anything signed by a server outside that chat's members is dropped
    - the owner removes a guest → their servers stop receiving new events; what they already copied stays with them, and the invite screen says so

## Alternatives
- Raft or Paxos across the cluster: every decision would need a majority. With two members, the Mac and the cloud, the majority is both, so closing the laptop would stop the cloud from answering; a third machine would exist only to vote. It buys strict agreement on everything, and the only decision that needs it is who routes, which the newest-copy rule plus a short claim window already covers.
- One leader for the whole cluster, the always-on member: the simplest, but the Mac alone stops working when the internet drops, and it breaks "every server still works alone".
- CRDTs for everything (Automerge-style documents): the chat log is already the simplest CRDT, a grow-only set in clock order; extending that to settings and memory adds a library for data that rarely changes and has one person who can decide.
- Matrix-style event graphs with state resolution: the closest existing model, built for servers that don't trust each other. Ours share an owner, so owner-signed settings replace state resolution. It becomes the right tool if two different people's clusters ever share a chat.
- Byzantine fault-tolerant protocols (PBFT, Tendermint): they guard against members that lie. All members are the owner's; a compromised one is removed, not outvoted.
- Total-order broadcast: every member sees the same order before anything shows. It costs a round trip per message and needs the members up, the same problem as Raft.

## Behaviour
- The laptop closes mid-chat: the cloud member routes the next message to one of its agents; the laptop's agents show unavailable and their tasks wait. The laptop opens: it pulls the events it missed, its agents show available, and waiting tasks carry on.
- The home internet drops while the phone is on home wifi: the phone talks to the Mac, which routes once the cloud's claim doesn't arrive; the cloud keeps the chat for anyone else. When the link returns the logs merge, and any double answer is marked.

## Open Questions
- In the app, should the cluster have a name at all, or appear only as "Your machines" in Settings?
