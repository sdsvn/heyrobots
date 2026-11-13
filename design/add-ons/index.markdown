---
co-review: design
---
# Rethink add-ons: any MCP server, skills from skills.sh, and an IT agent

## Context
Add-ons today are a hand-written list of 15 apps in the server's catalogue file, 6 of them greyed as "coming". Any other MCP server needs a pasted link. Skills come from a short built-in list or a pasted GitHub link. When an app connects, every one of its tools starts switched off until you review each one: Linear brought 78 tools and Notion 77, none were on, so an agent asked to "check Linear" had nothing to call and asked you to connect Linear, which you already had. Setting any of this up is your job alone; no agent helps.

The change: the catalogue comes from the official MCP registry, so any server listed there can be found and connected, as well as any you paste. Skills are searched on skills.sh. Tools work as soon as an app connects, under safe defaults. An IT agent on the team, or any agent you give the setup toolset, does most of this for you in a chat.

## Design
- Add-ons are found in the official MCP registry, with a short featured list on top
  - the server searches `registry.modelcontextprotocol.io` (latest versions only) and keeps results for a day
    - each result shows its name, what it does, its publisher (the namespace, such as `com.notion`) and how it runs: on the publisher's servers, or as a package on this computer
    - the catalogue file shrinks to a starter set for a team doing everyday work, each pinned to its publisher's own registry entry, plus what the registry can't list: your Google account and apps on this Mac (Messages, Calendar, Claude Code)
  - the starter set, all checked in the registry today
    - mail and calendar: Gmail and Google Calendar (your Google account)
    - tasks and projects: Linear, Jira and Confluence (Atlassian), monday.com, Todoist
    - notes and data: Notion, Airtable
    - code: GitHub, Supabase
    - design: Figma, Canva
    - money: Stripe
    - research: Exa for the web, Context7 for code docs
    - a browser: Playwright, a package that runs on this computer
    - everything else: Zapier, which reaches thousands of apps through one connection
    - Slack has no server of its own in the registry yet, so it stays findable by search and through Zapier
  - look-alikes are common: searching "notion" returns Notion's own server and four third-party ones
    - a server whose publisher matches its address (`com.notion` serving from `mcp.notion.com`) is marked official and ranked first
    - the publisher is always shown, so you can tell them apart
  - when the registry is down, the featured list and pasting a link still work
- Any MCP server can be connected, in three ways, all built in this change
  - a server run by its publisher: connect over HTTP, signing in with the server's own sign-in or a pasted token when it asks for one
    - sign-ins still return to `127.0.0.1` on the server, so they start on the server's computer; a phone can browse but not sign in
  - a package (npm or PyPI): run on this computer as a local server, through `npx` or `uvx`
    - the registry lists the settings it needs; secrets go into the vault, never into the database
    - it runs code as your user, so the sheet shows the exact command and "runs on this computer" before you confirm
    - container packages wait until heyrobots has a container runtime
  - anything else: paste an address or a command, as today
- Tools work as soon as an app connects, under defaults you can change
  - each tool starts from what the server says about it: a tool marked read-only is accepted and reads on its own; every other tool waits for your OK on each call, in Plans
  - an app's sheet changes all its tools at once (reads on their own / everything asks / off) or one tool at a time
  - a tool whose description changes after you connected goes back to asking, rather than off, until you look
  - what can go wrong: the read-only mark is the publisher's own claim
    - reads already run in quarantine, so their results are treated as untrusted
    - a tool that only claims to read could still change something without asking, so the sheet offers "everything asks" for apps you trust less
  - Linear's and Notion's tools, never reviewed today, get the same defaults when the server updates
- An agent says exactly what is missing
  - if a task's app has every tool off, or its sign-in ended, the agent is told in plain words and passes on how to fix it ("Linear is connected, but its tools are off: turn them on in Add-ons → Linear")
- Skills are searched on skills.sh
  - the server asks skills.sh's search for the name, the GitHub repository and the install count
  - adding one fetches its folder from GitHub, as a pasted link does today: same size limits, pinned to the commit it was fetched at, never updated on its own
  - a skill is instructions an agent follows with its own tools, so the sheet shows the repository and install count before you add it
- Setting up is a teammate's job: an IT agent on the Team tab, and any agent you trust with it
  - setup is a new toolset, "setup", that any agent can be given, such as your Chief of staff or a CTO
  - a built-in "IT" agent comes with it, shown on the Team tab like General; you can rename it or switch it off
  - it knows the setup: which add-ons and skills are connected, which agent has what, and what each needs; the lead sends "connect…", "add a skill for…" and "give the Coder GitHub" to an agent with the setup toolset
  - its tools search add-ons and skills, connect an add-on, change tool access, add a skill and give an add-on or skill to an agent
    - searching is free; each change is proposed and waits for your OK, like a new rule today
    - connecting answers a sign-in link, which opens on the server's computer; on a phone the agent says to open it there
  - only your own direct requests can change setup; a task that read outside content can't, as with rules today
  - the Add-ons and Skills screens get a "Set up with an agent" button that opens a chat with the IT agent
- The app's Add-ons and Skills screens become search-first
  - one search box over the featured list and the registry, or skills.sh for skills; sections and chips stay for browsing
  - mockups first, in the "heyrobots app — cute redesign" canvas, for your sign-off before building

## Alternatives
- Other registries (Smithery, Glama, PulseMCP): most need an account or a key, and Smithery already publishes into the official registry. The official one is free, open and the standard.
- Keep every tool off until reviewed: safest on paper, but it is why Linear did nothing today. With every non-read call waiting for your OK, the defaults are no less safe than before.
- Setup tools only on a hidden agent: simpler, but you couldn't hand setup to the Chief of staff or a CTO you already trust. A toolset lets any agent take it on.

## Open Questions
