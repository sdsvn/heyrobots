package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/workflow"
	"google.golang.org/genai"

	"heyrobots/service/internal/jobs"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/usage"
)

// jobHost gives a job's steps the run's model, tools and gateway, so a step
// has exactly what a free-form turn would.
type jobHost struct {
	r     *run
	llm   model.LLM
	tools []tool.Tool
	sets  []tool.Toolset
}

func (h *jobHost) ModelAgent(s jobs.Step) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:                     "step_" + strings.NewReplacer("/", "_", "-", "_").Replace(s.Name),
		Description:              "One step of a standing job.",
		Model:                    h.llm,
		Instruction:              h.r.e.Prompt + "\n\n" + h.r.agentSection() + "\n\n" + h.r.taskSection() + "\n\n# This step\n\n" + s.Instruction + "\n\nDo only this step; the job's other steps run before and after it. When done, answer with what you did, in a few lines, and don't call report.",
		Tools:                    h.tools,
		Toolsets:                 h.sets,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
		BeforeModelCallbacks:     []llmagent.BeforeModelCallback{h.r.countCalls},
		BeforeToolCallbacks:      []llmagent.BeforeToolCallback{h.r.guardTools},
	})
}

// CallTool runs a tool step through the same guard a model's call gets.
func (h *jobHost) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	for _, t := range h.tools {
		if t.Name() != name {
			continue
		}
		if res, err := h.r.gw.Guard(ctx, t, args); err != nil || res != nil {
			return res, err
		}
		ft, ok := t.(interface {
			Run(ctx agent.Context, args any) (map[string]any, error)
		})
		if !ok {
			return nil, fmt.Errorf("tool %s can't be called from a job step", name)
		}
		actx, ok := ctx.(agent.Context)
		if !ok {
			return nil, errors.New("a tool step needs the workflow's context")
		}
		return ft.Run(actx, args)
	}
	return nil, fmt.Errorf("the task has no tool named %s", name)
}

// runJob runs the task's standing job as an ADK workflow. A paused job
// ends the turn as a question; the person's reply resumes it.
func (r *run) runJob(ctx context.Context, name string, llm model.LLM, tools []tool.Tool, sets []tool.Toolset) (report, error) {
	e := r.e
	if e.Jobs == nil {
		return report{State: "failed", Summary: "Jobs aren't set up on this server."}, errors.New("no jobs library")
	}
	j, all, err := e.Jobs.Get(name)
	if err != nil {
		return report{State: "failed", Summary: "The job " + name + " couldn't be loaded: " + err.Error()}, err
	}
	ag, err := jobs.Build(j, all, &jobHost{r: r, llm: llm, tools: tools, sets: sets})
	if err != nil {
		return report{State: "failed", Summary: "The job " + name + " couldn't be built: " + err.Error()}, err
	}
	run, err := runner.New(runner.Config{AppName: appName, Agent: ag, SessionService: e.Sessions})
	if err != nil {
		return report{State: "failed", Summary: "The job couldn't start."}, err
	}
	sessionID := r.c.Ticket.ID
	if _, err := e.Sessions.Get(ctx, &session.GetRequest{AppName: appName, UserID: "assistant", SessionID: sessionID}); err != nil {
		if _, err := e.Sessions.Create(ctx, &session.CreateRequest{AppName: appName, UserID: "assistant", SessionID: sessionID}); err != nil {
			return report{State: "failed", Summary: "The task's history couldn't be opened."}, err
		}
	}
	// A reply to a paused step resumes the workflow; anything else starts
	// it. The interrupt id was kept on the last Assignment.
	content := genai.NewContentFromText(r.inputMessage(), genai.RoleUser)
	if prev := r.c.PausedAt; prev != "" && len(r.c.Inputs) > 0 {
		var answer []string
		for _, in := range r.c.Inputs {
			answer = append(answer, in.Text)
		}
		content = &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID: prev, Name: workflow.WorkflowInputFunctionCallName, Response: map[string]any{"response": strings.Join(answer, "\n")},
		}}}}
	}
	rctx, cancel := context.WithTimeout(usage.WithTicket(ctx, r.c.Ticket.ID), e.RunLimit)
	defer cancel()
	var (
		runErr   error
		pausedAt string
		question string
		last     string
	)
	for ev, err := range run.Run(rctx, "assistant", sessionID, content, agent.RunConfig{}) {
		if err != nil {
			runErr = err
			break
		}
		if ev.RequestedInput != nil {
			pausedAt, question = ev.RequestedInput.InterruptID, ev.RequestedInput.Message
		}
		if ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" && !p.Thought {
					last = p.Text
				}
			}
		}
	}
	r.mu.Lock()
	budget := r.budget
	r.mu.Unlock()
	switch {
	case errors.Is(context.Cause(ctx), errStopped):
		return report{State: "dropped", Summary: "Stopped by request."}, nil
	case budget != "":
		return report{State: "blocked", Summary: budget, HumanAction: "Raise the monthly budget in settings, then reply here and I'll carry on."}, nil
	case errors.Is(rctx.Err(), context.DeadlineExceeded):
		return report{State: "blocked", Summary: fmt.Sprintf("The job ran out of time (%s).", e.RunLimit)}, rctx.Err()
	case ctx.Err() != nil:
		return report{State: store.StateInterrupted}, ctx.Err()
	case runErr != nil:
		return report{State: "failed", Summary: "The job " + name + " failed: " + shorten(runErr.Error(), 300)}, runErr
	case pausedAt != "":
		return report{State: "blocked", Summary: "The job " + name + " is waiting for you.", HumanAction: question, PausedAt: pausedAt}, nil
	}
	return report{State: "ready", Summary: "Ran the job " + name + ".", Result: shorten(last, 8000)}, nil
}
