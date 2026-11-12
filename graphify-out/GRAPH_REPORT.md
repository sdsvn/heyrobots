# Graph Report - heyrobots  (2026-11-10)

## Corpus Check
- 2 files · ~5,234 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 35 nodes · 33 edges · 5 communities (3 shown, 2 thin omitted)
- Extraction: 100% EXTRACTED · 0% INFERRED · 0% AMBIGUOUS
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- heyrobots: a proactive personal assistant you run yourself
- Build a proactive GitHub coding agent on Google ADK
- 11. The server and the app
- 12. Stack
- 7. Toolsets

## God Nodes (most connected - your core abstractions)
1. `heyrobots: a proactive personal assistant you run yourself` - 16 edges
2. `Build a proactive GitHub coding agent on Google ADK` - 8 edges
3. `11. The server and the app` - 7 edges
4. `Architecture to implement` - 2 edges
5. `7. Toolsets` - 2 edges
6. `12. Stack` - 2 edges
7. `What it does` - 1 edges
8. `Design rules (do not relax these)` - 1 edges
9. `ADK mapping` - 1 edges
10. `Plan, with checkpoints` - 1 edges

## Surprising Connections (you probably didn't know these)
- None detected - all connections are within the same source files.

## Communities (5 total, 2 thin omitted)

### Community 0 - "heyrobots: a proactive personal assistant you run yourself"
Cohesion: 0.14
Nodes (13): 10. Privacy and cost, 13. Layout, 14. Path to a hosted service, 15. Build order, 1. Principle, 2. Rules kept from the prototype, 3. Terms, 4. Who can ask an Assistant for work (+5 more)

### Community 1 - "Build a proactive GitHub coding agent on Google ADK"
Cohesion: 0.20
Nodes (9): ADK mapping, Architecture to implement, Before you start, Build a proactive GitHub coding agent on Google ADK, Deliverables, Design rules (do not relax these), Plan, with checkpoints, References: read these first (+1 more)

### Community 2 - "11. The server and the app"
Cohesion: 0.29
Nodes (7): 11. The server and the app, Notifications, Pairing and devices, Reaching the server from the phone, The API, The Flutter app, The server

## Knowledge Gaps
- **2 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `heyrobots: a proactive personal assistant you run yourself` connect `heyrobots: a proactive personal assistant you run yourself` to `11. The server and the app`, `12. Stack`, `7. Toolsets`?**
  _High betweenness centrality (0.451) - this node is a cross-community bridge._
- **Should `heyrobots: a proactive personal assistant you run yourself` be split into smaller, more focused modules?**
  _Cohesion score 0.14285714285714285 - nodes in this community are weakly interconnected._
- **Why does `11. The server and the app` connect `11. The server and the app` to `heyrobots: a proactive personal assistant you run yourself`?**
  _High betweenness centrality (0.219) - this node is a cross-community bridge._