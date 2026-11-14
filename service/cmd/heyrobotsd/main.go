// Command heyrobotsd runs the heyrobots server.
package main

import (
	"encoding/json"
	"heyrobots/service/internal/cluster"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/registry"
	"slices"

	"github.com/glebarez/sqlite"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session/database"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"heyrobots/service/internal/acp"
	"heyrobots/service/internal/api"
	"heyrobots/service/internal/changes"
	"heyrobots/service/internal/config"
	"heyrobots/service/internal/devices"
	"heyrobots/service/internal/eval"
	"heyrobots/service/internal/executor"
	"heyrobots/service/internal/gateway"
	"heyrobots/service/internal/google"
	"heyrobots/service/internal/guard"
	"heyrobots/service/internal/intake"
	"heyrobots/service/internal/jobs"
	"heyrobots/service/internal/models"
	"heyrobots/service/internal/netguard"
	"heyrobots/service/internal/notify"
	"heyrobots/service/internal/outbox"
	"heyrobots/service/internal/quarantine"
	"heyrobots/service/internal/router"
	"heyrobots/service/internal/skills"
	"heyrobots/service/internal/sources/calendar"
	"heyrobots/service/internal/sources/mail"
	"heyrobots/service/internal/sources/schedule"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/telemetry"
	"heyrobots/service/internal/toolsets"
	"heyrobots/service/internal/usage"
	"heyrobots/service/internal/vault"
	"heyrobots/service/internal/webapp"
	"heyrobots/service/prompts"
)

func main() {
	run := run
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "pair":
			run = func() error { return pairCommand(os.Args[2:]) }
		case "status":
			run = statusCommand
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "heyrobotsd:", err)
		os.Exit(1)
	}
}

func run() error {
	level := slog.LevelInfo
	if os.Getenv("HEYROBOTS_DEBUG") != "" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Traces go out over OTLP only when OTEL_EXPORTER_OTLP_ENDPOINT is set.
	shutdownTraces, exporting, err := telemetry.Setup(ctx, version)
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTraces(sctx)
	}()
	if exporting {
		log.Info("exporting traces over OTLP", "endpoint", firstSet(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"), os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")))
	}

	if err := lockData(cfg.DataDir); err != nil {
		return err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	var ks vault.KeySource = vault.FileKey{Path: filepath.Join(cfg.DataDir, "vault.key")}
	if cfg.VaultKey == "keychain" {
		ks = vault.KeychainKey{Service: "heyrobots", Account: cfg.DataDir}
	}
	v, err := vault.Open(cfg.DataDir, ks)
	if err != nil {
		return err
	}

	// Each role runs on the model the owner chose in settings, or on the
	// configured Gemini models until they do.
	gem := func(name string) models.Choice { return models.Choice{Provider: models.Gemini, Model: name} }
	resolver := &models.Resolver{Store: st, Vault: v, Defaults: map[string]models.Choice{
		models.RoleRouter: gem(cfg.RouterModel), models.RoleReader: gem(cfg.RouterModel), models.RoleWorker: gem(cfg.WorkerModel),
	}}
	ledger := &usage.Ledger{Store: st, Prices: cfg.ModelPrices, Override: resolver.Price, Log: log}
	for _, m := range []string{cfg.RouterModel, cfg.WorkerModel} {
		if _, ok := cfg.ModelPrices[m]; !ok {
			log.Warn("no price known for this model; spending is estimated high until HEYROBOTS_MODEL_PRICES sets it", "model", m)
		}
	}
	smallModel := router.Metered(resolver.Model(models.RoleRouter), ledger.Record)
	engine := &eval.Engine{
		Store:  st,
		Log:    log,
		Router: &router.Router{Prompt: prompts.Router, Model: smallModel},
	}

	sessions, err := database.NewSessionService(sqlite.Open(st.DSN()), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	if err := database.AutoMigrate(sessions); err != nil {
		return fmt.Errorf("prepare session store: %w", err)
	}
	skillLib := &skills.Library{Dir: filepath.Join(cfg.DataDir, "skills")}
	// Skills heyrobots ships go in once, to the agent each is for.
	if added, err := skillLib.InstallBuiltins(ctx, filepath.Join(cfg.DataDir, "builtin-skills")); err != nil {
		log.Warn("built-in skills", "err", err)
	} else {
		for _, b := range added {
			if a, err := st.Agent(b.Agent); err == nil && !slices.Contains(a.SkillList(), b.Name) {
				sk, _ := json.Marshal(append(a.SkillList(), b.Name))
				a.Skills, a.UpdatedAt = string(sk), time.Now().UTC()
				if err := st.SaveAgent(a); err != nil {
					log.Warn("give built-in skill", "skill", b.Name, "err", err)
				}
			}
			log.Info("built-in skill added", "skill", b.Name, "agent", b.Agent)
		}
	}
	jobLib := &jobs.Library{Dir: filepath.Join(cfg.DataDir, "jobs")}
	engine.Jobs = func(name string) (eval.JobSpec, error) {
		j, _, err := jobLib.Get(name)
		if err != nil {
			return eval.JobSpec{}, err
		}
		return eval.JobSpec{Agent: j.Agent, Rule: j.Rule, Toolsets: j.Toolsets}, nil
	}
	reader := &quarantine.Reader{Model: router.Metered(resolver.Model(models.RoleReader), ledger.Record)}
	mcpServers := &gateway.MCP{Store: st, Vault: v, Reader: reader, Log: log}
	// The owner's own Claude Code, for agents whose brain it is.
	claude := &acp.Worker{Command: cfg.ACPCommand, ConfigDir: cfg.ClaudeConfigDir, Model: cfg.AgentModel, Log: log}
	// The owner's own ACP agent can route too, so no API key is needed.
	engine.Router = router.Either{
		Model: &router.Router{Prompt: prompts.Router, Model: smallModel},
		ACP:   &router.ACP{Prompt: prompts.Router, Ask: claude.Ask},
		UseACP: func() bool {
			c, err := resolver.Choice(models.RoleRouter)
			return err == nil && c.Provider == models.ClaudeCode
		},
	}
	mcpRegistry := &registry.Client{}
	var srv *api.Server
	workers := &executor.Engine{
		ACP: claude,
		UsesACP: func(a store.Agent) bool {
			p := a.Provider
			if p == "" {
				c, _ := resolver.Choice(models.RoleWorker)
				p = c.Provider
			}
			return p == models.ClaudeCode
		},
		Store:      st,
		Sessions:   sessions,
		Model:      router.Metered(resolver.Model(models.RoleWorker), ledger.Record),
		BudgetStop: ledger.BudgetStop,
		Reader:     reader,
		MCP:        mcpServers,
		Web:        netguard.Client(netguard.PublicWeb, 30*time.Second),
		Prompt:     prompts.Executor,
		Log:        log,
		Workspaces: filepath.Join(cfg.DataDir, "workspaces"),
		Files:      filepath.Join(cfg.DataDir, "files"),
		OnHandBack: engine.Kick,
		Skills:     skillLib,
		Jobs:       jobLib,
		Registry:   mcpRegistry,
		Memory:     filepath.Join(cfg.DataDir, "memory"),
		// An agent with its own model runs on it, metered like the rest.
		AgentModel: func(ctx context.Context, a store.Agent) (model.LLM, error) {
			return router.Metered(func(ctx context.Context) (model.LLM, error) {
				return resolver.ForChoice(ctx, models.Choice{Provider: a.Provider, Model: a.Model, BaseURL: a.BaseURL})
			}, ledger.Record)(ctx)
		},
	}
	// The owner's Google account, if connected, for mail and calendar.
	gg := &google.Google{Vault: v, Store: st, Ends: google.Live, HTTP: &http.Client{Timeout: time.Minute}}
	workers.Google = gg
	workers.Outward = map[string]toolsets.ProposalFunc{
		"send_email":    toolsets.SendEmailProposal,
		"create_event":  toolsets.CreateEventProposal,
		"answer_invite": toolsets.AnswerInviteProposal(gg),
	}
	_, port, err := net.SplitHostPort(cfg.Listen[0])
	if err != nil {
		return fmt.Errorf("listen address %q: %w", cfg.Listen[0], err)
	}
	googleRedirect := "http://127.0.0.1:" + port + "/oauth/google/callback"
	// Listen on this machine's tailnet address too, when it has one: only the
	// owner's own devices can reach it, and pairing codes lead with it.
	if !cfg.Public {
		cfg.Listen = withLoopback(cfg.Listen)
	}
	tailName := ""
	if ips := tailnetIPs(); len(ips) > 0 && !cfg.Public {
		cfg.Listen = withTailnet(cfg.Listen, ips)
		tailName = tailnetName(ips)
		log.Info("listening on the tailnet too", "addrs", ips, "name", tailName)
	}
	scheme := "http"
	if cfg.TLSCert != "" {
		scheme = "https"
	}

	ob := &outbox.Outbox{
		Store: st,
		Senders: map[string]outbox.Sender{
			"app":      outbox.AppSender{},
			"mail":     &mail.Sender{Google: gg, Store: st},
			"calendar": &calendar.Sender{Google: gg},
			"mcp":      gateway.Sender{MCP: mcpServers},
			changes.Source: changes.Sender{Store: st, Jobs: jobLib, Now: time.Now,
				// The API server is made below; approved setup changes run its steps.
				Connect:  func(ctx context.Context, id string) (string, string, error) { return srv.ConnectAddOn(ctx, id) },
				AddSkill: func(ctx context.Context, source string) (string, error) { return srv.InstallSkill(ctx, source) }},
		},
		Log: log,
	}

	in := intake.New(st, time.Now)
	// The owner's other machines share the team; alone, this is one member.
	addr := cfg.ClusterAddress
	if addr == "" {
		if host, _, err := net.SplitHostPort(cfg.ClusterListen); err == nil && host != "" && host != "0.0.0.0" && host != "::" {
			addr = cfg.ClusterListen
		}
	}
	node, err := cluster.New(st, in, log, cfg.MachineName, addr, policy.AppOwner.String())
	if err != nil {
		return err
	}
	engine.Available = node.Available
	engine.HandOff = node.HandOff
	mailSrc := &mail.Source{Google: gg, Intake: in, Store: st, Log: log, OnNew: engine.Kick}
	calSrc := &calendar.Source{Google: gg, Intake: in, Store: st, Log: log, OnNew: engine.Kick}
	schedSrc := &schedule.Source{Store: st, Intake: in, Log: log, Now: time.Now, OnNew: engine.Kick, Jobs: jobLib, Tools: mcpServers}

	opts := api.Options{
		Usage:  ledger,
		Intake: in,
		Google: gg, GoogleRedirect: googleRedirect,
		MCPRedirect: "http://127.0.0.1:" + port + "/oauth/mcp/callback",
		OnAccounts: func() {
			mailSrc.Kick()
			calSrc.Kick()
		},
		OnMessage: func() {
			engine.Kick()
			workers.Kick()
		},
		Store:     st,
		Cluster:   node,
		Devices:   devices.New(st, time.Now),
		Vault:     v,
		MCP:       mcpServers,
		Registry:  mcpRegistry,
		Files:     filepath.Join(cfg.DataDir, "files"),
		Models:    resolver,
		ACP:       claude,
		Skills:    skillLib,
		Jobs:      jobLib,
		Broker:    api.NewBroker(),
		Log:       log,
		ServerURL: cfg.ServerURL,
		Addresses: reachable(cfg.ServerURL, cfg.Listen, scheme, tailName),
		ShowPairingCode: func(code string, expires time.Time, servers []string) {
			fmt.Fprint(os.Stderr, pairingBlock(code, expires, servers, scheme+"://"+localAddr(cfg.Listen[0])))
			log.Info("pairing code issued; it is printed above")
		},
		ShowSetupCode: func(code string) {
			fmt.Fprintf(os.Stderr, "\n  heyrobots setup code: %s\n  Open http://%s/setup on this machine and enter it.\n\n", code, cfg.Listen[0])
			log.Info("setup code issued; it is printed above")
		},
	}
	if cfg.Public {
		g, err := guard.New(ctx, cfg.Guard, nil)
		if err != nil {
			return err
		}
		opts.Guard = g.Middleware
		log.Info("public mode: every request needs the access proxy's signed identity", "issuer", cfg.Guard.Issuer)
	}
	pusher := &notify.Notifier{
		Store:  st,
		Token:  func() string { t, _ := v.Get(vault.NtfyToken); return t },
		Client: &http.Client{Timeout: 20 * time.Second},
		Log:    log,
		Now:    time.Now,
	}
	opts.Pusher = pusher
	if web, ok := webapp.FS(cfg.WebDir); ok {
		opts.Web = webapp.Handler(web)
		log.Info("serving the web app", "from", firstSet(cfg.WebDir, "embedded build"))
	}
	srv = api.New(opts)
	srv.SetOnDecided(func() {
		ob.Kick()
		workers.Kick()
		pusher.Kick()
	})
	engine.OnDecided = func(threadID string) {
		srv.ThreadUpdated(threadID)
		workers.CancelStopped()
		workers.Kick()
		ob.Kick()
	}
	workers.OnChanged = func(threadID string) {
		srv.ThreadUpdated(threadID)
		ob.Kick()
		pusher.Kick()
	}
	ob.OnChanged = func(threadID string) {
		srv.ThreadUpdated(threadID)
		pusher.Kick()
	}
	ob.OnDigest = func(d store.Digest) {
		srv.DigestReady(d)
		pusher.Kick()
	}
	node.Hooks = cluster.Hooks{
		ThreadChanged: srv.ThreadUpdated,
		KickEval:      engine.Kick,
		Decide: func(effectID uint, approve bool, note string, verified bool, from string) error {
			var threadID string
			err := st.Tx(func(tx *store.Tx) error {
				e, err := outbox.Decide(tx, outbox.Decision{EffectID: effectID, Approve: approve, DeviceID: "machine:" + from, Note: note, UserVerified: verified, At: time.Now()})
				threadID = e.ThreadID
				return err
			})
			if err != nil {
				return err
			}
			ob.Kick()
			workers.Kick()
			srv.ThreadUpdated(threadID)
			return nil
		},
	}
	if err := srv.AnnounceSetup(); err != nil {
		return err
	}

	handler := srv.Handler()
	var servers []*http.Server
	errc := make(chan error, len(cfg.Listen))
	for _, addr := range cfg.Listen {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		hs := &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       2 * time.Minute,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
			// Requests share the server's context, so open event streams end
			// on shutdown instead of holding it up.
			BaseContext: func(net.Listener) context.Context { return ctx },
		}
		servers = append(servers, hs)
		log.Info("listening", "addr", ln.Addr().String(), "tls", cfg.TLSCert != "")
		go func() {
			var err error
			if cfg.TLSCert != "" {
				err = hs.ServeTLS(ln, cfg.TLSCert, cfg.TLSKey)
			} else {
				err = hs.Serve(ln)
			}
			if !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}

	// With your own agent as the brain, keep one adapter started and ready,
	// so a reply never waits for one to boot.
	for _, role := range []string{models.RoleWorker, models.RoleRouter} {
		if c, err := resolver.Choice(role); err == nil && c.Provider == models.ClaudeCode {
			claude.Warm()
			break
		}
	}
	go node.Run(ctx)
	if cfg.ClusterListen != "" {
		go func() {
			log.Info("machine link listening", "addr", cfg.ClusterListen, "member", node.ID(), "name", node.Name())
			if err := node.Listen(ctx, cfg.ClusterListen); err != nil {
				errc <- fmt.Errorf("machine link: %w", err)
			}
		}()
	}
	go cleanupCodes(ctx, st, log)
	go engine.Run(ctx)
	go workers.Run(ctx)
	go ob.Run(ctx)
	go pusher.Run(ctx)
	go mailSrc.Run(ctx)
	go calSrc.Run(ctx)
	go schedSrc.Run(ctx)
	var chosen []any
	for _, role := range models.Roles {
		if c, err := resolver.Choice(role); err == nil {
			chosen = append(chosen, role, c.Provider+"/"+c.Model)
		}
	}
	log.Info("eval and executors running", chosen...)

	select {
	case <-ctx.Done():
	case err := <-errc:
		stop()
		return err
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, hs := range servers {
		hs.Shutdown(shutdownCtx)
	}
	return nil
}

// version is the server's version, for traces.
const version = "0.1.0"

func firstSet(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func cleanupCodes(ctx context.Context, st *store.Store, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := st.DeleteExpiredPairingCodes(time.Now().UTC().Add(-24 * time.Hour)); err != nil {
				log.Warn("clean up pairing codes", "err", err)
			}
		}
	}
}
