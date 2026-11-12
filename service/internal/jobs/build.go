package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/workflowagent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/workflow"
)

// Host is what the executor gives a job's steps: how a model step thinks,
// how a tool step calls, and how an ask step reaches the owner.
type Host interface {
	// ModelAgent returns an agent that does one model step with the
	// instruction. The agent's own identity, skills and tools apply.
	ModelAgent(step Step) (agent.Agent, error)
	// CallTool calls one tool by name with the arguments, through the
	// gateway, and returns what it answered.
	CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error)
}

// StepOutput is what a step hands the next one: a short text, so steps
// can be read by a person and fed to a model.
type StepOutput struct {
	Step string `json:"step"`
	Text string `json:"text"`
}

// InterruptPrefix starts every ask step's interrupt id, so the executor
// can tell a job's pause from anything else.
const InterruptPrefix = "ask-"

// Build turns a job into an ADK agent that runs its steps in order. all
// holds the library, for job steps.
func Build(j Job, all map[string]Job, h Host) (agent.Agent, error) {
	edges, err := edgesFor(j, all, h, []string{j.Name})
	if err != nil {
		return nil, err
	}
	return workflowagent.New(workflowagent.Config{Name: "job-" + j.Name, Description: j.Description, Edges: edges})
}

func edgesFor(j Job, all map[string]Job, h Host, path []string) ([]workflow.Edge, error) {
	nodes := []workflow.Node{workflow.Start}
	for _, s := range j.Steps {
		n, err := nodeFor(j, s, all, h, path)
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", s.Name, err)
		}
		nodes = append(nodes, n)
	}
	return workflow.Chain(nodes...), nil
}

func nodeFor(j Job, s Step, all map[string]Job, h Host, path []string) (workflow.Node, error) {
	cfg := workflow.NodeConfig{}
	if s.Retry > 0 {
		rc := workflow.DefaultRetryConfig()
		rc.MaxAttempts = s.Retry + 1
		cfg.RetryConfig = rc
	}
	if s.Timeout != "" {
		d, err := time.ParseDuration(s.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout: %w", err)
		}
		cfg.Timeout = d
	}
	// Node names must be unique in the whole graph, sub-jobs included.
	name := strings.Join(append(append([]string{}, path[1:]...), s.Name), "/")
	switch s.Kind {
	case KindModel:
		ag, err := h.ModelAgent(Step{Name: name, Kind: s.Kind, Instruction: j.Body + "\n\n" + s.Instruction})
		if err != nil {
			return nil, err
		}
		return workflow.NewAgentNode(ag, cfg)
	case KindTool:
		tool, args := s.Tool, s.Args
		return workflow.NewFunctionNode(name, func(ctx agent.Context, in any) (StepOutput, error) {
			res, err := h.CallTool(ctx, tool, args)
			if err != nil {
				return StepOutput{}, err
			}
			if e, ok := res["error"].(string); ok && e != "" {
				return StepOutput{}, errors.New(e)
			}
			return StepOutput{Step: name, Text: fmt.Sprint(res)}, nil
		}, cfg), nil
	case KindAsk:
		q := s.Question
		id := InterruptPrefix + name
		return workflow.NewEmittingFunctionNode(name, func(ctx agent.Context, in any, emit func(*session.Event) error) (StepOutput, error) {
			reply, err := workflow.ResumeOrRequestInput(ctx, emit, session.RequestInput{InterruptID: id, Message: q})
			if err != nil {
				return StepOutput{}, err
			}
			return StepOutput{Step: name, Text: fmt.Sprint(reply)}, nil
		}, cfg), nil
	case KindJob:
		child, ok := all[s.Job]
		if !ok {
			return nil, fmt.Errorf("job %s doesn't exist", s.Job)
		}
		edges, err := edgesFor(child, all, h, append(path, s.Job))
		if err != nil {
			return nil, err
		}
		return workflow.NewWorkflowNode(name, edges)
	}
	return nil, fmt.Errorf("unknown kind %q", s.Kind)
}
