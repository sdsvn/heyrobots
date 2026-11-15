# What makes an agent a specialist: frameworks and evidence

Research for `design/specialists/index.markdown`, done 2026-11-15. It uses primary sources only: official docs, specs, framework source and the original papers. Every claim cites a source with a tag such as `[A2]`, and the tags are listed under Sources. Numbers are as reported by their authors. Fetched pages were read through a summariser, so the important figures were re-checked against verbatim text or tables where that was possible. Where a figure could not be confirmed, the claim gives only its direction.

## Summary

| Framework / work | What a specialist is | How it's declared | How it's selected | How output is checked | How it's evaluated |
|---|---|---|---|---|---|
| Anthropic patterns [A1] | A specialised prompt/model after a router, or a worker under an orchestrator | Code, not a format | Routing: an LLM or classifier "classifies an input and directs it to a specialized followup task" | Evaluator-optimizer loop, when clear criteria exist | Not specified |
| Anthropic Research [A2] | A lead (Opus 4) plus subagents (Sonnet 4) and a CitationAgent | Prompts. Each delegation states an objective, output format, tool guidance and boundaries | The lead plans and spawns 3–5 in parallel | The CitationAgent attributes claims; the lead decides whether more research is needed | About 20 real queries; an LLM judge with a rubric (0–1 scores plus pass/fail); human testing |
| Claude Code subagents [A3][A4] | A system prompt plus tool allowlist/denylist, model, skills, MCP servers, memory, hooks, maxTurns, effort, permissionMode | `.claude/agents/*.md`: YAML frontmatter and a body, or `AgentDefinition` in the SDK | Claude matches the task against `description`; also explicit by name | Not built in (hooks can do it) | Not specified |
| Agent Skills [A5] | Know-how, not an agent: `SKILL.md` plus `scripts/`, `references/`, `assets/` | Frontmatter: `name` and `description` required; `license`, `compatibility`, `metadata`, `allowed-tools` optional | The model reads name+description (~100 tokens per skill) and loads the body on activation | — | `skills-ref validate` checks format only |
| OpenAI Agents SDK [O1–O4] | `Agent`: instructions, tools, handoffs, `output_type`, input and output guardrails, model | Python objects | Handoffs appear as `transfer_to_<agent>` tools whose descriptions include `handoff_description`; or the manager calls agents through `as_tool` | Output guardrails with a tripwire that halts the run; tool guardrails | None found in the pages read |
| Google ADK 2.0 [G1–G7] | `LlmAgent` (instruction, tools, `input_schema`/`output_schema`, mode), composed into workflows | Code or a YAML Agent Config | `description` is "primarily used by other LLM agents to determine if they should route a task"; graph edges route on node output; experimental `RoutedAgent` (a code function) | `LoopAgent` with a critic that escalates or `exit_loop`; per-node retry and timeout; HITL nodes | `.test.json`/evalset files; tool trajectory match, rubric judges, hallucination and safety criteria |
| A2A 1.0 [P1] | A remote agent described by an `AgentCard` holding `skills[]` | Protobuf/JSON card | Clients read the card: skill `description`, `tags`, `examples`, input/output modes | Out of scope | Out of scope |
| CrewAI [C1–C3] | Agent with `role`, `goal`, `backstory`, tools, llm, knowledge, memory; Task with `description`, `expected_output` | `agents.yaml`/`tasks.yaml`, now JSONC | Tasks are assigned explicitly; optional delegation | Task `guardrail(s)`: a function `(bool, Any)` or an LLM criterion string; retries up to `guardrail_max_retries` (default 3) | `crewai test`: N runs, a 1–10 score per task |
| AutoGen / Magentic-One [M1][M2] | Participants with a `description`; Magentic-One has WebSurfer, FileSurfer, Coder, ComputerTerminal | Code | A selector LLM picks the next speaker from `name : description` plus history; Magentic-One's Orchestrator uses a Progress Ledger | The Orchestrator asks each step whether the task is done and whether progress is being made; it replans after stalls | GAIA, AssistantBench, WebArena; AutoGenBench |
| LangGraph supervisor [L1][L2] | Sub-agents behind handoff tools | Code | A supervisor LLM calls `transfer_to`/`assign_to` tools | None built in | LangChain's τ-bench study with distractor domains |
| MetaGPT [R1] | A role that follows an SOP and publishes one structured artifact (PRD, design, task list, code, tests) | Code (roles and actions) | Publish/subscribe: a role acts when its prerequisite artifacts arrive | Executable feedback: run tests, debug, at most 3 retries | HumanEval and MBPP; role ablation |
| ChatDev [R2] | Roles (CEO, CTO, programmer, reviewer, tester) in a phased chat chain | Code and config | A fixed chain of phases | Review and test phases; "communicative dehallucination" | Completeness, executability, consistency, quality; ablations |

**Bottom line.** Across these sources a specialist differs in three structural ways, not in persona: its tool scope, its output contract, and the verifier its output passes. Its routing card (description plus examples) decides whether it gets work at all. The evidence for specialisation is strongest for verification and structured handoffs. It is weakest for splitting sequential work across agents, and token cost rises at every split.

---

## 1. Anthropic

**Building effective agents** (Dec 2024) [A1]
- It separates *workflows*, where "LLMs and tools are orchestrated through predefined code paths", from *agents*, where LLMs "dynamically direct their own processes and tool usage". In these terms, heyrobots' draft method is a **workflow**: routing, then a fixed chain, then an evaluator loop.
- **Routing** lets each downstream prompt or model specialise. The router can be an LLM or a classic classifier, and easy queries can go to smaller models.
- **Orchestrator-workers** fits work whose subtasks can't be known in advance.
- **Evaluator-optimizer** fits when there are "clear evaluation criteria" and an LLM can give useful feedback, the kind a human reviewer would give.
- Its advice is to start with the simplest system and add agentic complexity only when it pays. It also says tool and interface design deserves as much effort as the prompt ("poka-yoke your tools").

**Multi-agent research system** (Jun 2025) [A2]
- **What a specialist is.** Subagents act as filters with their own context, and a separate CitationAgent attributes claims to sources. Each delegation must state an **objective, output format, tool and source guidance, and task boundaries**. Vague delegation led to duplicated work.
- **Evidence for.** The multi-agent setup beat single-agent Opus 4 by **90.2%** on the internal research eval.
- **Evidence on cost.** On BrowseComp, token usage alone explained **80%** of performance variance. Tool calls and model choice brought the total to 95%. Agents use about **4×** the tokens of chat, and multi-agent systems about **15×**.
- **Not a fit:** domains that need shared context or have many dependencies between agents, and "most coding tasks".
- **Failures:** spawning 50 subagents for simple queries and endless searching. The fix was scaling rules written into the prompt: one agent with 3–10 tool calls for simple fact-finding.
- **Evaluation:** start with about 20 real queries. Use one LLM judge call with a rubric (factual accuracy, citation accuracy, completeness, source quality, tool efficiency) that outputs 0–1 scores plus pass/fail. Humans caught source-quality problems the judge missed.

**Context engineering** (Sep 2025) [A6]: a subagent returns "a condensed, distilled summary… (often 1,000-2,000 tokens)". Multi-agent suits research and analysis, compaction suits long back-and-forth, and structured notes suit milestone-based work.

**Building multi-agent systems: when and how** (Jan 2026) [A7]
- It names three conditions where multiple agents consistently win: **context protection, parallelization, specialization** (focused tools or prompts). Outside these, coordination usually costs more than it gains.
- Multi-agent typically uses **3–10×** the tokens for equivalent tasks.
- It recommends starting with a single agent.
- **Split by context, not by work type.** Poor boundaries are "sequential phases of the same work", tightly coupled components and shared state. Good boundaries are independent research paths, clean interfaces and **blackbox verification**.
- A **verification subagent** works well because it needs little context.
- Named failures:
  - **telephone game**: information degrades as it passes between agents.
  - **early victory problem**: the verifier runs a few tests and declares success. Mitigations are concrete criteria, negative tests, and an instruction to run the full suite.
  - **context pollution**.

**Claude Code subagents** [A3]
- **Declaration.** A Markdown file whose frontmatter needs only `name` and `description`. Optional fields: `tools`, `disallowedTools`, `model`, `permissionMode`, `maxTurns`, `skills`, `mcpServers`, `hooks`, `memory`, `effort`, `isolation`, `background`. The body becomes the system prompt.
- **Selection.** Claude matches the task against `description` ("use proactively" encourages delegation). Explicit invocation by name also works. A startup warning appears when all descriptions together exceed about 15k tokens.
- **Context.** Each subagent starts fresh, and only its final summary returns to the caller.
- **Best practice:** one clear job per agent, the fewest tools, cheaper models for simple work, and "don't overspawn".

**Claude Agent SDK** [A4]
- **Declaration.** `AgentDefinition` has the same fields as the file format (`description`, `prompt`, `tools`, `model`, `skills`, `memory`, `mcpServers`, `maxTurns`, `effort`, `permissionMode`, …).
- **Benefits it lists:** context isolation, parallelism, specialised instructions, and tool restrictions. A tool left out of the list is simply absent from the session.
- **Cost controls:** spawn depth (default 3), concurrency (default 20) and `maxBudgetUsd`.

**Agent Skills spec** [A5]
- A skill is a directory: `SKILL.md` (frontmatter `name` ≤64 chars and `description` ≤1024 chars, required) plus `scripts/`, `references/` and `assets/`.
- **Progressive disclosure:** metadata (~100 tokens) loads at startup, the body (<5k tokens recommended) loads on activation, and resources load on demand.
- A skill is *know-how*, not an agent. It has no method, verifier or routing beyond its description.

## 2. OpenAI Agents SDK

- **What a specialist is.** `Agent` fields: `name`, `instructions` (static or a function), `tools`, `mcp_servers`, `handoffs`, `handoff_description`, `model`/`model_settings`, `input_guardrails`/`output_guardrails`, `output_type` (structured output), `hooks`, `tool_use_behavior` [O1].
- **Two composition styles** [O1]:
  - **Handoffs** are decentralised: the specialist takes over and by default receives the full history.
  - **Agents-as-tools** keep a manager that "retains control".
- **Handoffs** [O2] are tools named `transfer_to_<agent>`, and their description includes `handoff_description`. Options:
  - `input_type` lets the model pass structured arguments.
  - `on_handoff` can reject a transfer.
  - `input_filter` can trim history (for example `remove_all_tools`).
- **`as_tool`** [O4] takes structured `parameters`. Its `custom_output_extractor` shapes what returns to the manager, and the nested run doesn't inherit the parent's conversation.
- **Verification** [O3]:
  - Input guardrails run on the first agent only, either in parallel or blocking.
  - Output guardrails run on the final agent's output; a tripwire raises an exception and halts the run.
  - Tool guardrails run on every function-tool call.
  - These guardrails are pass/halt checks, not a retry-with-feedback loop.
- **When to split** (*A practical guide to building agents*) [O5]: "maximize a single agent's capabilities first". Split for *complex logic* (many if-then branches) or *tool overload*. Overload depends on tool similarity, not count: some systems handle more than 15 distinct tools, while others struggle with fewer than 10 overlapping ones.

## 3. Google ADK and A2A

- **Declaration** [G1][G6]: `LlmAgent` fields `name`, `description`, `model`, `instruction`, `tools`, plus `input_schema`/`output_schema`. Python and Java can also use a YAML Agent Config (`name`, `model`, `description`, `instruction`, tools, sub-agents).
- **Routing by description** [G1]: `description` is "primarily used by *other* LLM agents to determine if they should route a task to this agent". It should be specific enough to tell peers apart: "Handles inquiries about current billing statements", not "Billing agent".
- **Collaboration modes** (ADK 2.0) [G2]: `sub_agents` on a coordinator auto-generate a delegation tool per sub-agent. Each sub-agent has a `mode`:
  - `chat` (default): full user interaction; returns to the parent only by manual transfer.
  - `task`: may ask the user to clarify; returns automatically via `finish_task`.
  - `single_turn`: no user interaction; returns automatically and can run in parallel.
- **Workflow styles** (ADK 2.0) [G3]:
  - **Graph workflows** combine LLM agents, functions, tools and human-input nodes. Edges route on a node's output, and each node can have retries and a timeout. The docs claim graphs give "improved precision and reliability over purely prompt-based agents" because the logic is explicit.
  - **Template workflow agents** are `SequentialAgent`, `ParallelAgent` and `LoopAgent` [G4]. A `LoopAgent` doesn't decide when to stop by itself. A critic sub-agent escalates (`exit_loop`) or `max_iterations` caps it, for example a `WriterAgent`/`CriticAgent` loop with `max_iterations=5`.
- **Routing** [G7]: the experimental `RoutedAgent` (TypeScript) picks one agent per call with a code function and calls the function again with error context to choose a fallback.
- **Evaluation** [G5]:
  - It evaluates the final response and the *trajectory* (the tool calls taken).
  - `.test.json` files serve as unit tests; evalsets with multi-turn sessions or simulated users serve as integration tests.
  - Criteria include `tool_trajectory_avg_score`, `final_response_match_v2`, `rubric_based_final_response_quality_v1`, `rubric_based_tool_use_quality_v1`, `hallucinations_v1` and `safety_v1`.
- **A2A 1.0 AgentCard** [P1] (`specification/a2a.proto`):
  - The card has `name`, `description`, `version`, `capabilities`, `default_input_modes`, `default_output_modes`, `skills[]`, security schemes and signatures.
  - **`AgentSkill`** has `id`, `name`, `description`, `tags` (required), `examples` ("Example prompts or scenarios that this skill can handle"), per-skill `input_modes`/`output_modes`, and `security_requirements`.
  - The spec calls skills "largely a descriptive concept but represents a more focused set of behaviors that the agent is likely to succeed at". It defines no "not for" field.

## 4. CrewAI, AutoGen / Magentic-One, LangGraph

**CrewAI** [C1][C2][C3]
- **Agent** requires `role`, `goal`, `backstory`. Optional: `tools`, `llm`, `allow_delegation`, `knowledge_sources`, `memory`, `reasoning`, `max_iter` (20), `max_retry_limit`. Declared in YAML or JSONC.
- **Task** has `description`, **`expected_output`**, `agent`, `context` (earlier tasks whose output feeds it), `output_pydantic`/`output_json`, `human_input`.
- **Guardrails** are either a function returning `(True, result)`/`(False, error)` or an LLM criterion string checked by the agent's own LLM. They chain in order. On failure the error goes back to the agent, up to `guardrail_max_retries` (default 3).
- **Evaluation:** `crewai test` runs the crew N times (default 3) and reports a 1–10 score per task and time per run.

**AutoGen** [M1]
- `SelectorGroupChat`: each turn, a model picks the next speaker from `{roles}` (`name : description`) and `{history}`.
- `candidate_func` narrows the candidates, and `selector_func` can override the model's pick in code.
- Termination conditions such as `MaxMessageTermination` cap the run.
- Advice: keep descriptions distinct. If the selector prompt fills with per-agent conditions, use a selector function or split the work.

**Magentic-One** [M2] (arXiv 2411.04468v1)
- **Two ledgers:**
  - The **Task Ledger** (outer loop) holds facts, guesses and the plan.
  - The **Progress Ledger** (inner loop) answers five questions every step: done? looping? progressing? who next? what instruction?
  - A stall counter (threshold ≤2) triggers reflection and a new plan.
- **Specialists:** WebSurfer, FileSurfer (read-only), Coder, ComputerTerminal (deterministic execution).
- **Ablations (verbatim):** "without the full ledgers, performance drops by 31%". Removing any single worker "reduces performance by between 21% (Coder, Executor) to 39% (FileSurfer)".
- **Error analysis:** the top failures were persistent inefficient actions and **insufficient verification steps**.
- **Modularity claim:** agents can be added or removed without retuning prompts.

**LangGraph** [L1][L2]
- The `langgraph-supervisor` README now recommends building the supervisor pattern "directly via tools" for more control over context engineering.
- By default, handoff passes the **full** message history; `output_mode` chooses between `full_history` and `last_message`.
- **LangChain's benchmark** [L2] (Jun 2025): τ-bench retail, gpt-4o, with up to six irrelevant "distractor" domains of 19 tools each added.
  - A single agent with every domain's tools degraded sharply from two distractor domains on, and its token use grew.
  - Swarm and supervisor setups held flat.
  - The supervisor lost points to "translation", meaning it paraphrased sub-agent replies. Removing handoff messages from sub-agent context and adding a `forward_message` tool (pass a reply through verbatim) gave a reported "nearly 50%" gain.

## 5. Research

**MetaGPT** [R1] (arXiv 2308.00352v7)
- Human **SOPs** are encoded as prompt sequences.
- Each role publishes a **structured artifact** to a shared message pool:
  - PM: a PRD
  - Architect: design, interfaces and file list
  - Project Manager: a task list
  - Engineer: code
  - QA: tests
- Roles subscribe only to what they need and act once their prerequisites arrive.
- **Executable feedback** runs tests and debugs, up to 3 retries. It added 4.2 points (HumanEval) and 5.4 points (MBPP) to Pass@1, reaching 85.9% and 87.7%.
- Role ablation (Table 3): going from Engineer-only to four roles raised cost from $0.915 to $1.385 and executability from 1.0 to 4.0, and cut revisions from 10 to 2.5.
- The paper attributes failures of naive multi-agent systems to "cascading hallucinations caused by naively chaining LLMs".

**ChatDev** [R2] (ACL 2024, arXiv 2307.07924v5)
- A **chat chain** of phases (design → coding → testing), each run as instructor/assistant pairs.
- "Communicative dehallucination": the assistant asks for specifics before answering.
- **Quality** rose from 0.1523 (MetaGPT) to 0.3953.
- **Ablations:**
  - Removing role prompts cut quality to 0.2212, the largest drop.
  - Removing dehallucination cut it to 0.3094.
  - Halting after coding cut it to 0.2512.

**MAST: "Why Do Multi-Agent LLM Systems Fail?"** [R3] (Cemri et al., arXiv 2503.13657v3)
- The taxonomy was built from 150 traces with human agreement κ = 0.88. MAST-Data now holds 1600+ traces across 7 frameworks.
- **14 failure modes in 3 categories:**
  - **System design:** disobey task spec, disobey role spec, step repetition, loss of history, unaware of termination.
  - **Inter-agent misalignment:** conversation reset, fail to ask for clarification, task derailment, information withholding, ignored other agent's input, reasoning-action mismatch.
  - **Task verification:** premature termination, no or incomplete verification, **incorrect verification**.
- Disobeying the *role* spec is rare (~1.5%). Step repetition, reasoning-action mismatch, termination and verification failures dominate.
- **Interventions (Table 5):**
  - ChatDev on ProgramDev: baseline 25.0, improved role prompts 34.4, new topology (cyclic graph) 40.6.
  - AG2 MathChat with GPT-4o: baseline 84.25, prompt with a verification section 89.00, new topology with a Verifier agent 88.83.
  - With GPT-4, the topology change was not significant (p = 0.4).
  - The authors say the ChatDev fixes "do not constitute substantial improvements". They conclude that tactical prompt and topology fixes are inconsistent and that structural changes, especially stronger verification and communication protocols, are needed.

**Kim et al., "Towards a Science of Scaling Agent Systems"** [R4] (Google/MIT, arXiv 2512.08296v3)
- 180 controlled configurations across 3 model families. Against a single-agent baseline, the multi-agent change ranges "from +80.8% on decomposable financial reasoning to −70.0% on sequential planning".
- Coordination gives diminishing returns once the single-agent baseline is already strong (**capability saturation**).
- **Tool-heavy tasks incur multi-agent overhead.**
- Architectures *without centralized verification* propagate errors more.
- A predictive model picks the best architecture for 87% of held-out configurations.

**Cognition, "Don't Build Multi-Agents"** [R5] (Walden Yan, Jun 12 2025; their position)
- Principles: (1) share full agent traces, not just messages; (2) "actions carry implicit decisions", and parallel agents make conflicting ones.
- Example: in a Flappy Bird clone split across two subagents, one built a Mario-style background and the other a mismatched bird.
- Recommendation: a single-threaded linear agent, plus a context-compression model for long tasks.
- It notes that Claude Code's subagents answer narrow questions rather than write code.

## 6. Synthesis

### Dimensions along which specialists genuinely differ

| Dimension | What it means | Who has it | Evidence it matters |
|---|---|---|---|
| **Method / SOP** | A fixed sequence of steps, phases or graph | MetaGPT SOPs, ChatDev chain, ADK graph/Sequential/Loop, CrewAI tasks, Anthropic workflows | ChatDev halt-early ablations [R2]; MAST topology changes helped but inconsistently [R3]; MetaGPT fewer revisions [R1] |
| **Tool scope** | An allowlist/denylist per agent or step | Claude Code `tools`/`disallowedTools`, Agent SDK, OpenAI `tools`, ADK `tools`, Magentic-One (read-only FileSurfer) | Single-agent collapse with distractor tools [L2]; tool overlap guidance [O5]; least-privilege safety [A3] |
| **Output contract** | A schema or "expected output" the step must produce | OpenAI `output_type`, ADK `output_schema`, CrewAI `expected_output`/`output_pydantic`, MetaGPT artifacts, A2A output modes | MetaGPT structured artifacts against cascading hallucination [R1]; delegation must state output format [A2] |
| **Verifier** | A check that gates output, with feedback and retry | CrewAI guardrails, OpenAI output guardrails, ADK LoopAgent critic, MetaGPT executable feedback, Anthropic evaluator-optimizer, CitationAgent | MAST: one of three failure categories; Magentic-One: second-commonest error [M2]; centralized verification limits error propagation [R4]; +4–5 Pass@1 from tests [R1] |
| **Knowledge** | Reference material loaded on demand | Agent Skills `references/`, CrewAI `knowledge_sources`, Claude Code `skills` | Progressive disclosure keeps context small [A5]; context pollution [A7] |
| **Model** | A different model per agent or step | Claude Code `model`, Agent SDK, Anthropic routing, Research (Opus lead, Sonnet workers) | Model choice is one of three factors behind 95% of variance [A2] |
| **Memory** | Persistent per-agent state | Claude Code `memory`, CrewAI `memory`, LangGraph store | Supported by every framework; no ablation found |
| **Routing card** | Description, tags, examples (and negatives) | A2A `AgentSkill`, ADK/Claude Code/AutoGen `description`, OpenAI `handoff_description` | Every LLM router reads only this; distinct descriptions advised [G1][M1][A3] |
| **Interaction mode** | Whether it may talk to the user and when it returns | ADK `chat`/`task`/`single_turn`, OpenAI handoff vs as_tool | Supervisor "translation" losses [L2] |
| **Evaluation set** | Its own test cases and rubric | ADK evalsets, Anthropic ~20 queries plus rubric, CrewAI `crewai test` | Rubric judge consistent with humans [A2]; humans still needed |

Persona (role, goal, backstory) is the one dimension every framework has and the one with the weakest evidence. ChatDev's "remove roles" ablation [R2] is the main support for it. MAST, though, finds role disobedience rare and prompt fixes inconsistent [R3].

### Known failure modes and mitigations

| Failure | Evidence | Mitigation in the sources |
|---|---|---|
| **Handoff context loss / implicit decisions** | Flappy Bird [R5]; telephone game [A7]; supervisor paraphrase [L2]; MAST withholding/reset [R3] | Structured artifacts [R1]; explicit objective, format and boundaries in each delegation [A2]; `forward_message` verbatim [L2]; split only by context, not by phase [A7] |
| **Coordination overhead and loops** | MAST step repetition and termination [R3]; Magentic-One inefficient actions [M2]; −70% on sequential tasks [R4] | Progress-ledger stall detection [M2]; `max_iterations` and termination conditions [G4][M1]; prompt scaling rules [A2] |
| **Cost multiplication** | 4× agent / 15× multi-agent [A2]; 3–10× [A7] | Budget, depth and concurrency caps [A4]; cheap models for gathering [A2][A3]; spend only where the task's value justifies it [A2] |
| **Router misrouting / under-delegation** | Single agent degrades with distractors [L2]; delegation needs good descriptions [A3][A4] | Specific, distinct descriptions with examples [G1][P1]; narrowing candidates in code (`candidate_func`) [M1]; fallback on error [G7] |
| **Wrong or weak verification** | MAST incorrect and incomplete verification [R3]; early victory [A7] | Tool-based checks (tests) [R1]; concrete criteria and negative tests [A7]; blocking guardrails before side effects [O3]; human spot checks [A2] |
| **Over-spawning on easy tasks** | 50 subagents for simple queries [A2] | Effort scaled to complexity in the prompt [A2]; triage step that answers directly |

### What this means for the draft

- The draft builds a **workflow-specialist**: a router plus a per-agent method, per-step tools, a check loop and a recorded outcome. It is not a parallel multi-agent system.
- So the strongest multi-agent numbers (+90.2% [A2], +80.8% [R4]) **do not transfer**: they come from parallel breadth search.
- What does transfer:
  - verification gates [R1][R3][C2][O3]
  - structured artifacts between steps [R1]
  - narrowed tools per step [L2][O5]
  - per-step model choice [A2]
  - a description-based card [G1][P1]
- What cuts **against** the draft:
  - [A7] lists "sequential phases of the same work" as a *poor* split, and the Researcher's gather → write → check is exactly that.
  - [R5] warns that file-only handoffs drop implicit decisions.
  - [R4] finds coordination helps less once a single agent is already strong.
  - [O5] advises maximising one agent first.
  - [R3] shows a check step can itself be wrong.

## Recommendations for heyrobots

1. **Make the card an A2A-shaped skill list, plus a "not for" line, and test it.**
   - Draft line: *"the frontmatter holds its card: its name, what it is good at, a few example requests, and what it is not for"*.
   - Mirror `AgentSkill` (`id`, `description`, `tags`, `examples`) [P1] so a remote teammate's card could later be served as an A2A card.
   - Keep descriptions distinct from one another [G1][M1], and keep all cards together small; Claude Code warns above ~15k tokens [A3].
   - "Not for" has no counterpart in any spec. Treat it as a heyrobots addition and prove it works: every card example, and every recorded hand-back, becomes a router test case in `service/internal/eval` (the routing step, not quality evaluation).

2. **Make the check step the core of a specialist, and make it hard to fool.**
   - Draft line: *"A new kind of step checks the work against the specialist's own standard."*
   - Prefer **tool checks** (run tests, open links, verify a citation exists) over model checks. Executable feedback is the best-evidenced gain [R1].
   - For model checks, use Anthropic's form: a rubric with 0–1 scores plus pass/fail in one call [A2]. Write concrete criteria and at least one negative test per checklist to avoid "early victory" [A7].
   - A fresh-context checker is well supported, since blackbox verification needs little context [A7].
   - Record checker false passes in `agent_outcomes` (owner re-asked after a pass), because "incorrect verification" is a known failure mode [R3].
   - Keep the retry cap; the field uses 2–3 [C2][R1][G4].

3. **Give every method an output contract, not just instructions.**
   - Add an `expected_output` (prose) and an optional schema per specialist and per step, like CrewAI `expected_output`/`output_pydantic` [C2], OpenAI `output_type` [O1] and ADK `output_schema` [G3].
   - The check step validates the schema in code first; the rubric comes second.

4. **Make the "take it in" brief a structured artifact that carries decisions, and keep sequential phases few.**
   - Draft line: *"writes the brief the other steps work from"* and *"steps hand their work on through the task folder"*.
   - Following MetaGPT [R1] and Anthropic's delegation fields [A2], the brief should state: objective, criteria, **assumptions and decisions made**, out-of-scope, output format and tool guidance. This answers Cognition's implicit-decision point [R5].
   - Start each package with the fewest steps (take it in → work → check). Add a separate gather or write step only when its eval shows a gain, because [A7] calls phase splits a poor boundary and [R4] measured losses on sequential tasks.
   - Steps that read the brief plus notes, rather than a summary of a summary, avoid the telephone game.

5. **Narrow tools and pick models per step, and measure the cost.**
   - Draft line: *"a step lists the tools it may call… a step can use its own model."*
   - This is the best-supported dimension: tool overload [L2][O5], least privilege [A3][A4], cheap gather / strong write [A2].
   - Record tokens per step in `agent_outcomes`. Multi-step runs multiply tokens (3–10× [A7]), and token use drives quality [A2], so a "cheaper" method can be worse.

6. **Ship each package with its own small eval set, and gate it against General.**
   - Draft line: *"Each task records how the specialist did (`agent_outcomes`)… the router doesn't use it yet."*
   - Outcomes are telemetry, not an eval. Give each package `evals/` with about 20 real requests [A2], each with a rubric and, where useful, the expected tool trajectory [G5].
   - Run it against General on the same model. Ship or keep a package only if it beats General: single-agent-first is the advice in [O5][A7], and capability saturation [R4] means it may not win on a strong model.
   - This also answers the draft's open question about what to build first: build what the eval shows General getting wrong.

7. **Keep triage and hand-back cheap, and pass state forward verbatim.**
   - Draft line: *"the method's first step is always take it in… a specialist may hand a task back."*
   - The direct-answer path matches Anthropic's scaling rules for simple queries [A2].
   - On hand-back, pass the router the brief and reason, not the transcript; trim as OpenAI's `input_filter` does [O2].
   - When a specialist's report reaches the owner, forward it as written rather than re-summarising it (`forward_message`) [L2].

8. **Bound loops and stalls the way Magentic-One does.**
   - Draft line: *"retries, timeouts and pauses for the owner work as they do for jobs."*
   - Add a progress check between retries: is the check failing for the *same* reasons again? If so, stop early and report, rather than spending the second try. This is the Progress Ledger stall idea [M2] and targets MAST's top failure, step repetition [R3].

## Sources

- [A1] Anthropic, *Building effective agents*, Dec 2024. https://www.anthropic.com/engineering/building-effective-agents
- [A2] Anthropic, *How we built our multi-agent research system*, Jun 2025. https://www.anthropic.com/engineering/multi-agent-research-system
- [A3] Claude Code docs, *Subagents*. https://code.claude.com/docs/en/sub-agents
- [A4] Claude Agent SDK docs, *Subagents in the SDK*. https://code.claude.com/docs/en/agent-sdk/subagents
- [A5] Agent Skills, *Specification*. https://agentskills.io/specification
- [A6] Anthropic, *Effective context engineering for AI agents*, Sep 29 2025. https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- [A7] Anthropic (C. Phillips et al.), *Building multi-agent systems: When and how to use them*, Jan 23 2026. https://claude.com/resources/articles/building-multi-agent-systems-when-and-how-to-use-them
- [O1] OpenAI Agents SDK, *Agents*. https://openai.github.io/openai-agents-python/agents/
- [O2] OpenAI Agents SDK, *Handoffs*. https://openai.github.io/openai-agents-python/handoffs/
- [O3] OpenAI Agents SDK, *Guardrails*. https://openai.github.io/openai-agents-python/guardrails/
- [O4] OpenAI Agents SDK, *Tools (agents as tools)*. https://openai.github.io/openai-agents-python/tools/
- [O5] OpenAI, *A practical guide to building agents* (PDF), "When to consider creating multiple agents". https://cdn.openai.com/business-guides-and-resources/a-practical-guide-to-building-agents.pdf
- [G1] ADK docs, *LLM agents* (`docs/agents/llm-agents.md`). https://github.com/google/adk-docs
- [G2] ADK docs, *Build collaborative agent teams* (`docs/workflows/collaboration.md`, ADK 2.0)
- [G3] ADK docs, *Graph-based agent workflows* (`docs/graphs/index.md`, `routes.md`, `retry.md`, `human-input.md`, `docs/workflows/index.md`)
- [G4] ADK docs, *Loop agents* (`docs/agents/workflow-agents/loop-agents.md`)
- [G5] ADK docs, *Why evaluate agents* and *Evaluation criteria* (`docs/evaluate/index.md`, `criteria.md`)
- [G6] ADK docs, *Agent Config* (`docs/agents/config.md`)
- [G7] ADK docs, *Route between agents* (`docs/agents/routing.md`, experimental, TypeScript)
- [P1] A2A Protocol v1.0.0, `specification/a2a.proto` (`AgentCard`, `AgentSkill`). https://github.com/a2aproject/A2A/blob/main/specification/a2a.proto
- [C1] CrewAI docs, *Agents*. https://docs.crewai.com/en/concepts/agents
- [C2] CrewAI docs, *Tasks*. https://docs.crewai.com/en/concepts/tasks
- [C3] CrewAI docs, *Testing*. https://docs.crewai.com/en/concepts/testing
- [M1] AutoGen AgentChat docs, *Selector Group Chat*. https://microsoft.github.io/autogen/stable/user-guide/agentchat-user-guide/selector-group-chat.html
- [M2] Fourney et al., *Magentic-One*, arXiv 2411.04468v1. https://arxiv.org/html/2411.04468v1
- [L1] `langgraph-supervisor-py` README. https://github.com/langchain-ai/langgraph-supervisor-py
- [L2] LangChain, *Benchmarking multi-agent architectures*, Jun 10 2025. https://www.langchain.com/blog/benchmarking-multi-agent-architectures
- [R1] Hong et al., *MetaGPT*, arXiv 2308.00352v7. https://arxiv.org/html/2308.00352v7
- [R2] Qian et al., *ChatDev: Communicative Agents for Software Development*, ACL 2024, arXiv 2307.07924v5. https://arxiv.org/html/2307.07924v5
- [R3] Cemri et al., *Why Do Multi-Agent LLM Systems Fail?*, arXiv 2503.13657v3. https://arxiv.org/html/2503.13657v3
- [R4] Kim et al., *Towards a Science of Scaling Agent Systems*, arXiv 2512.08296v3. https://arxiv.org/abs/2512.08296
- [R5] W. Yan (Cognition), *Don't Build Multi-Agents*, Jun 12 2025. https://cognition.com/blog/dont-build-multi-agents
