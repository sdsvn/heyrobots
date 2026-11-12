// Command adkdev loads the router agent into ADK's own development UI, to
// try prompts and see tool calls. It is for development only: it records
// routing calls and acts on none of them, and the server never uses it.
//
//	GOOGLE_API_KEY=... go run ./cmd/adkdev web
package main

import (
	"context"
	"log"
	"os"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/cmd/launcher/full"

	"heyrobots/service/internal/config"
	"heyrobots/service/internal/router"
	"heyrobots/service/prompts"
)

func main() {
	ctx := context.Background()
	name := os.Getenv("HEYROBOTS_ROUTER_MODEL")
	if name == "" {
		name = config.DefaultRouterModel
	}
	model, err := router.Gemini(name, func() (string, bool) {
		k := os.Getenv("GOOGLE_API_KEY")
		return k, k != ""
	})(ctx)
	if err != nil {
		log.Fatal(err)
	}
	a, err := router.DevAgent(model, prompts.Router)
	if err != nil {
		log.Fatal(err)
	}
	l := full.NewLauncher()
	if err := l.Execute(ctx, &launcher.Config{AgentLoader: agent.NewSingleLoader(a)}, os.Args[1:]); err != nil {
		log.Fatalf("%v\n\n%s", err, l.CommandLineSyntax())
	}
}
