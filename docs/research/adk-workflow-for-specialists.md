# ADK Go v2.7.1 workflow engine vs. the specialists design

Sources: ADK Go v2.7.1 from the module cache (`~/go/pkg/mod/google.golang.org/adk/v2@v2.7.1/`). Paths below are relative to that module root. Design under test: `design/specialists/index.markdown`. heyrobots code: `service/internal/jobs/build.go`, `service/internal/executor/job.go`.

## Summary

| Capability | Supported? | How |
|---|---|---|
| 1. Take-in step answers and ends, or continues | Yes | A function node emits `Event.Routes`; edges use `EdgeBuilder.AddRoute` / `Default`. A route with no matching edge ends the branch. An LLM node can't route by itself. |
| 2. Check fails, goes back to a named earlier step, at most N tries | Yes for the loop, no for the cap | Conditional back-edges are allowed. The check's `Output` becomes the earlier node's input. The engine keeps no loop counter, so you keep it (in state). `RetryConfig` is a different thing. |
| 3. Tools and model per step | Yes | Each `llmagent.New(Config{Model, Tools, Toolsets})` wrapped by `workflow.NewAgentNode`. The workflow adds nothing and takes nothing away. |
| 4. Fresh check call with no history | Yes, by default | An LlmAgent at a graph node runs `single_turn`, which sees only its node input plus its own turn. Give it no tools and a non-nil input. Or call `model.LLM` directly in a function node. |
| 5. Passing data between steps | Yes | Primary: predecessor `Output`, which becomes the successor's input. Also session state through `Actions.StateDelta` / `OutputKey`, read with `{key}` or `NewFunctionNodeFromState`. |
| 6. Resume after a restart mid-workflow | Only for owner pauses | `Resume` rebuilds state only for an answered interrupt. A crash mid-step starts a fresh `Run` from START. Retry `Attempt` isn't rebuilt. Loop counts exist only if you put them in state. |
| 7. External process (ACP / Claude Code) step | Yes | `NewFunctionNode` / `NewEmittingFunctionNode` with `NodeConfig.Timeout`. The per-node ctx is cancelled on timeout or stop. The body must honour `ctx.Done()`. |

## 1. Conditional branching / ending early

- Routing is data on the event. `workflow/workflow.go: type Route interface { Matches(*session.Event) bool }` is implemented by `StringRoute`, `IntRoute`, `BoolRoute` and `MultiRoute`, and each one matches against `session.Event.Routes` (`session/session.go: Event.Routes`). `Default` fires when no concrete route matched (`workflow/workflow.go: var Default`).
- Edges are added with `workflow/edgebuilder.go: (*EdgeBuilder).AddRoute(from, to, route)` and `AddRoutes`. `AddRoutes` can't express `Default`, so add that edge with `AddRoute`.
- The dispatch rules are in `workflow/scheduler.go: findSuccessors`:
  - An unconditional edge always fires.
  - A concrete route fires when it matches.
  - `Default` fires only when nothing concrete matched.
  - "If every outgoing edge has a concrete Route and none matched, and no Default is present, the workflow silently dead-ends at this node", and this is not an error. Ending early therefore needs no special API.
- A node sets at most one routing event per activation (`scheduler.go: ErrMultipleRoutingEvents`) and at most one output (`ErrMultipleOutputs`).
- A function node routes by returning a `*session.Event` with `Routes` set (`workflow/function_node.go: (*FunctionNode).Run`: "a custom FunctionNode can explicitly route its output (via event.Routes)"). It can also `emit` one from `NewEmittingFunctionNode`.
- An LlmAgent node cannot set `Routes`; nothing in `agent/llmagent` writes it. The official pattern is LLM node → function node that parses the reply → routes (`examples/workflow/routing/llm/main.go: routeByClassification`). An experimental alternative is `exp/classifier: NewNode`, a routing node backed by a classifier, which `workflow/validation.go: validateNodeOutgoingEdges` validates. Its package doc says "API and behavior may change".
- Several terminal nodes are fine as long as only one produces output in a run (`scheduler.go: (*scheduler).finalize`, `ErrMultipleTerminalOutputs`).
- Sketch for heyrobots:
  - Edges: `START → intake (AgentNode) → intake_route (FunctionNode)`.
  - Then `AddRoute(intake_route, answer, StringRoute("answer"))` and `AddRoute(intake_route, gather, StringRoute("continue"))`.
  - `intake` uses `OutputSchema` or a strict instruction (`{"route":"answer|continue","text":...}`). `intake_route` returns an event with `Routes=[route]` and `Output=text`.
  - Alternatively, give the "answer" route no edge, so the run dead-ends and `runJob` reports the last text.

## 2. Loop back to a named earlier step, bounded

- Cycles are allowed if at least one edge in them is conditional (`workflow/validation.go: validateCycles`, `ErrUnconditionalCycle`; `Default` counts as conditional).
- A loop-back target may have one unconditional incoming edge plus conditional ones (`validation.go: validateFanIn`, whose comment says "conditional fan-in and loop-back back-edges ... are not rejected").
- `workflow/retry_loop_test.go: TestScheduler_RetryLoop` is the reference: `{From: nodeA, To: nodeA, Route: StringRoute("loop")}` plus a `"finish"` route out.
- Node state is keyed by node name. A re-activation resets `Output`, `Input` and `Status` (`scheduler.go: (*scheduler).startNode`). On completion `ResumedInputs` is cleared, so "a future activation (if any, e.g. via loop-back routing) starts a fresh lifecycle" (`handleCompletion`).
- **Passing reasons back works.** The successor's input is the routing node's output (`handleCompletion`: `input = nr.output` → `findSuccessors(..., input, routingEv, ...)`). The check's router returns one event with both `Routes=["retry_write"]` and `Output={reasons...}`, and the earlier node receives that as its input. If the router returns only `Routes` with nil output (as in `examples/workflow/routing/llm`), the successor gets nil.
- **There is no built-in cap.** Nothing in `workflow/scheduler.go` counts iterations, and a cycle runs until a route leaves it or the parent ctx ends. Bound it yourself:
  - Keep a counter in session state (`Actions.StateDelta["check_tries"]`). The router reads it via `ctx.ReadonlyState()` or `NewFunctionNodeFromState` and routes `"give_up"` when it reaches N.
  - The heyrobots run-level `RunLimit` timeout and `countCalls` budget remain the backstop.
- **`RetryConfig` is not a loop.** It re-runs the *same* node with the *same* input when the node *returns an error* (`scheduler.go: handleCompletion` → `ShouldRetry` → `scheduleRetry(currentNode, ns.Input, ...)`).
  - `Attempt` resets to 0 on success (`ns.Attempt = 0`, verified by `TestScheduler_RetryLoop`).
  - The default predicate retries everything except `ErrInputValidation` (`workflow/config.go: defaultShouldRetry`). That includes `context.DeadlineExceeded` from a node timeout. Only `context.Canceled` and a dead parent ctx are excluded (`handleCompletion`).
  - It cannot target another node and cannot carry reasons.
- `agent/workflowagents/loopagent` (`Config.MaxIterations`, exit on `Actions.Escalate`) loops a fixed sequence of sub-agents. It is not a graph back-edge, and it doesn't fit "go back to a named step".

## 3. Per-node tools and per-node model

- `workflow/agent_node.go: NewAgentNode(a agent.Agent, cfg)` wraps any agent. Each node is its own `llmagent.New(llmagent.Config{...})`, which takes its own `Model model.LLM`, `Tools []tool.Tool`, `Toolsets []tool.Toolset`, callbacks and `OutputSchema` (`agent/llmagent/llmagent.go: type Config`).
- The workflow does not merge or override them. `AgentNode.Run` builds an invocation context with `Agent: n.agent`, and the agent's flow uses its own tools and model.
- What the workflow does impose (`workflow/validation.go`):
  - `validateNoTaskModeGraphNodes`: a static node cannot be `ModeTask`.
  - `validateChatModeWiring`: a `ModeChat` agent may only follow `Start`.
  - Unset mode resolves to `single_turn` at a graph node (`agentNodeMode`; `llmagent.Config.Mode` doc: "ModeSingleTurn as a node in a workflow").
  - Node names must be unique.
- For a `single_turn` node the identity line ("You are an agent...") is not added (`internal/llminternal/identity_request_processor.go`).
- heyrobots today builds every model step with the same `h.llm`, `h.tools` and `h.sets` (`executor/job.go: (*jobHost).ModelAgent`). Per-step narrowing is purely a heyrobots change: filter `Tools`/`Toolsets` (`tool/tool.go: FilterToolset`, `StringPredicate` wrap a toolset to a name allow-list) and pick the `Model` per step.

## 4. A fresh, history-free check call

- History reaches a model through the contents request processor (`internal/llminternal/contents_processor.go: ContentsRequestProcessor`).
  - A `single_turn` placement, or `IncludeContents: IncludeContentsNone`, selects `buildContentsCurrentTurnContextOnly`. Only an explicit `IncludeContentsDefault` opts back into full history (`placementHidesHistory`).
  - Events are additionally filtered by branch and isolation scope (`buildContentsDefaultWithCallSource`).
- How a node's turn is seeded (`agent/llmagent/llm_agent_wrapper.go: RunLLMAgentAsNode`, `PrepareLLMAgentInput`, `newWrappedSession`):
  - The node input becomes a user-authored seed event inserted at the end of the session view.
  - "Current turn" means from the latest user-authored or other-agent event onward (`buildContentsCurrentTurnContextOnly`). So the model sees the seed (brief + result + checklist) plus its own tool events, and nothing earlier, including its own previous activations in a loop.
  - The seed lives on a wrapped session view and is not appended to the stored session.
- **Gotcha: nil input breaks isolation.** If the node input is nil, `PrepareLLMAgentInput` returns no seed. The pivot then becomes the previous node's reply (converted to "For context:" by `ConvertForeignEvent`), or the whole history when there is none. Always hand the check node a non-nil input.
- The system instruction is still the agent's own `Instruction`, plus any `GlobalInstruction`. For a check, build a dedicated agent:
  - no `Tools`/`Toolsets`
  - `Instruction` = the checklist only (not `jobHost`'s task/agent sections)
  - optionally `IncludeContents: llmagent.IncludeContentsNone` to be explicit
  - `OutputSchema` for `{pass, reasons}`
- Alternative: a function node that calls `model.LLM.GenerateContent` with a hand-built `LLMRequest`. This is fully isolated, but it skips llmagent callbacks, so heyrobots' `countCalls`/budget and `guardTools` would not see it unless called by hand.

## 5. Passing data between nodes

- **Primary, idiomatic: node output becomes the successor's input.**
  - An AgentNode's final model text is promoted to `Event.Output` (`agent_node.go: synthesizeAgentOutput`). Its schema is validated via `ValidateOutput` / `OutputSchema`.
  - A FunctionNode's return value becomes `Event.Output` (`function_node.go: Run`).
  - The scheduler hands it to the successors (`scheduler.go: handleCompletion`).
  - `JoinNode` aggregates `map[pred]output` (`scheduler.go: appendSuccessor`).
  - heyrobots already relies on this (`jobs.StepOutput`).
- **Session state.**
  - `llmagent.Config.OutputKey` writes the reply into `Actions.StateDelta[OutputKey]` (`agent/llmagent/llm_agent_wrapper.go: ProcessLLMAgentOutput`).
  - A function node can return or emit an event with `Actions.StateDelta` (`workflow/dynamic_node_test.go: TestDynamicNode_EmitMidBody`).
  - Readers: `{key}` placeholders in an llmagent `Instruction` (`util/instructionutil/instruction.go: InjectSessionState`; not applied when `InstructionProvider` is used) and `workflow.NewFunctionNodeFromState` (struct fields bound from state; `state:"node_input"` gets the predecessor output).
  - The database service persists `StateDelta` on append (`session/database/service.go: extractStateDeltas`).
  - I did not trace whether `ctx.State().Set(...)` inside a plain FunctionNode reaches a persisted event (`agent/common_context.go: callbackContextState.Set` writes the in-memory state and `actions.StateDelta` only when actions exist). Use explicit `Actions.StateDelta` on an emitted event for anything that must survive.
- Only a predecessor's output is passed along an edge. A node two hops back is not visible, so the brief must be re-sent in each output or read from state or files. The design's "steps hand their work on through the task folder" fits this and also keeps `NodeState` JSON-small (`workflow/state.go: NodeState` asks for JSON-encodable Input/Output).

## 6. Resume after process restart

- `RunState` is not stored. It is rebuilt from session events (`workflow/persistence.go: (*Workflow).ReconstructRunState`, `workflow/state.go: RunState`).
- Resume happens only when the inbound message is a FunctionResponse to an open `RequestInput`:
  - `agent/workflowagent/workflow.go: (*workflowAgent).detectResume`
  - `runner/run_node.go`: `buildResumeResponses` → `wf.Resume` else `wf.Run`
- The runner reuses the paused run's invocation ID only in that case (`runner/run_node.go: resolveInvocationID`: returns "" for a fresh turn).
- `ReconstructRunState` returns nil when "no node has interrupt or failure history".
- **Crash mid-step (no pause):** heyrobots ends the run as `StateInterrupted` and the next claim recovers it as a follow-up (`internal/executor/control.go: finish`, `internal/store/assignments.go: Recovered`). `runJob` then sends plain text (`PausedAt` is empty), so the engine runs `Workflow.Run` from START with a new invocation ID, and every completed step runs again. This is how jobs behave today too.
- **Owner pause (ask step):** `Resume` finds the waiting node by interrupt ID.
  - Completed predecessors are restored with their outputs.
  - Already-run successors are skipped (`RunState.completed`).
  - Handoff vs re-entry follows `NodeConfig.RerunOnResume` (`workflow/resume.go: (*Workflow).Resume`; `persistence.go: inferNodeState`).
- **Counters across resume:**
  - `NodeState.Attempt` (retries) is not rebuilt; `inferNodeState` never sets it. A resumed node gets a full retry budget again.
  - Outputs are collected by node name (`collectNodeOutputs`), so in a loop a later activation's output wins and earlier ones are not distinguished.
  - A loop counter kept in session state does persist, because it is ordinary state. That also means it survives a fresh re-run from START, so reset it in the first step.
- **Graph changes:** `workflow/workflow.go: New` carries a TODO saying a graph change between deploys "silently corrupts the resume path"; no graph fingerprint is recorded.

## 7. A non-LLM, long-running external step (ACP / Claude Code)

- Use `workflow.NewFunctionNode` or `NewEmittingFunctionNode` (`function_node.go`). The body gets `agent.Context`, which is a `context.Context`.
- `NodeConfig.Timeout` wraps each activation in `WithAgentTimeout`; otherwise it gets `WithAgentCancel`, and the context inherits from the workflow's parent ctx (`scheduler.go: (*scheduler).startNode`; `config.go: NodeConfig.Timeout`). `TestScheduler_NodeTimeout` checks that `ctx.Done()` fires and the run fails with `context.DeadlineExceeded`.
- The emitting variant can stream progress events (no `Output`) before the single output. Non-partial events block until persisted (`scheduler.go: runNode`). It can also pause for the owner via `ResumeOrRequestInput`, as heyrobots' ask step does.
- Gotchas:
  - **Cancellation is cooperative.** Each node runs in a goroutine that `RunNode` waits on (`s.wg.Wait()`; `scheduler.go: runNode` has `defer wg.Done()`). A body that ignores ctx keeps the whole run, and the task, from returning. The ACP client call must abort the session on `ctx.Done()`.
  - With `RetryConfig` set, a timeout is retried by default (see §2). For a harness step, set `ShouldRetry` to exclude `context.DeadlineExceeded`, or use no retry.
  - A step killed by a restart re-runs from START (see §6). The ACP session's work in the task folder survives, so make the harness step idempotent (resume from files).
  - Input and Output must be JSON-encodable (`state.go: NodeState`).

## Design implications

1. **"take it in ... answers right away and ends"**: works, but needs two nodes, because an LLM node can't emit routes. Use `intake` (AgentNode with `OutputSchema` `{route, text}`) followed by `intake_route` (FunctionNode emitting `Routes` + `Output`), or the experimental `exp/classifier.Node`.
2. **"a fail sends the reasons back to a named earlier step, up to N tries"**:
   - Use a check router whose single event carries `Routes` and `Output`.
   - Use a back-edge `AddRoute(check_route, write, StringRoute("retry:write"))`.
   - Keep a tries counter in `StateDelta` that the router reads and turns into `"give_up"` → `report`.
   - Do not use `RetryConfig` for this.
   - The earlier step gets only the reasons, so it must re-read the brief and notes from the task folder or state.
3. **"a model check is run as a fresh call with no tools and no history"**: satisfied by a dedicated single_turn LlmAgent with no tools, a checklist-only instruction and a non-nil input. Do not reuse `jobHost.ModelAgent`, which injects the agent/task sections and all tools. Keep its `BeforeModelCallbacks: countCalls` so tries "count against the task's call limit and budget". A raw `model.LLM` call in a function node would bypass that.
4. **"retries, timeouts and pauses ... work as they do for jobs, across restarts too" / "The server restarts mid-method. The method resumes from the step it was on"**: **not true as written.**
   - Only owner pauses resume at the step. A crash mid-step restarts the method from START with a new invocation, and retry counts reset.
   - Simplest alternative: make steps idempotent over the task folder (skip work whose note or draft already exists), and reset the loop counter at intake.
   - If you need true step-level resume, write a checkpoint (last completed step plus its output) to state or the store. On recovery, start the run at that step by routing from intake, using a `"resume:<step>"` route.
5. **"A package is updated. A running task finishes on the method it started with"**: the engine records no graph version, and the TODO in `workflow.New` warns that a changed graph corrupts resume. heyrobots must pin the package version on the task (or Assignment) and rebuild that exact graph for resume turns.
6. **"each step reaches only what it needs" / "its own model"**: fully a heyrobots change. Give each step its own `llmagent.Config{Model, Tools, Toolsets}`, filtered by Mandate ∩ agent toolsets, and keep `guardTools`/`countCalls` on every step agent. The engine imposes nothing.
7. **"harness" step**: an emitting FunctionNode around the ACP client, with `NodeConfig.Timeout`, ctx-aware abort, and no default retry on timeout. Restart semantics are as in item 4.
