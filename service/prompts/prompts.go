// Package prompts holds the agents' system prompts as Markdown files.
package prompts

import _ "embed"

// Router is the router agent's system prompt.
//
//go:embed router.md
var Router string

// Executor is the worker agent's system prompt.
//
//go:embed executor.md
var Executor string
