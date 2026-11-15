// Command heyrobotsd runs the heyrobots server.
package main

import (
	"encoding/json"
	"heyrobots/service/internal/brain"
	"heyrobots/service/internal/cluster"
	"heyrobots/service/internal/policy"
	"heyrobots/service/internal/registry"
	"slices"
	"strings"
	"sync"

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
	"heyrobots/service/internal/specialists"
	"heyrobots/service/internal/store"
	"heyrobots/service/internal/telemetry"
	"heyrobots/service/internal/toolsets"
	"heyrobots/service/internal/update"
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
		case "version", "--version", "-v":
			fmt.Println(version)
			return
		case "help", "--help", "-h":
			helpCommand()
			return
		case "config":
			run = configCommand
		case "machines":
			run = func() error { return machinesCommand(os.Args[2:]) }
		default:
			// Anything else would start a server by mistake.
			fmt.Fprintf(os.Stderr, "heyrobotsd: unknown command %q\n\n", os.Args[1])
			helpCommand()
			os.Exit(2)
		}
	}
	err := run()
	if errors.Is(err, errRestart) {
		// An update replaced this binary: become the new one, as the same
		// process, so launchd or systemd carries on as if nothing happened.
		exe, xerr := os.Executable()
		if xerr == nil {
			xerr = syscall.Exec(exe, os.Args, os.Environ())
		}
		err = fmt.Errorf("restart after the update: %w; start heyrobotsd again", xerr)
	}
	if err != nil {
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
			if p == models.ClaudeCode {
				return true
			}
			// A specialist that works in Claude Code does so when it is
			// signed in here, and in the built-in loop otherwise.
			if s, ok := specialists.Get(a.Specialist); ok && s.Runtime == specialists.ClaudeCode && a.Provider == "" {
				return claudeReady.ok(claude)
			}
			return false
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

	// Updates from the app: the new release replaces this binary, and this
	// process then restarts as it once running tasks are done.
	restarting := false
	updater := &update.Updater{Version: version, Repo: "sdsvn/heyrobots", Log: log,
		HTTP: netguard.Client(netguard.PublicWeb, 10*time.Minute),
		Drain: func(dctx context.Context) func() {
			workers.Hold(true)
			for end := time.Now().Add(2 * time.Minute); workers.Running() > 0 && time.Now().Before(end) && dctx.Err() == nil; {
				time.Sleep(time.Second)
			}
			return func() { workers.Hold(false) }
		},
		Restart: func() { restarting = true; stop() },
		Working: workers.Running,
	}
	// Where the install script put the Mac app, when not the usual place.
	if d := os.Getenv("HEYROBOTS_APP_DIR"); d != "" {
		updater.AppDirs = []string{d}
	}
	// A release server of your own, such as a local copy for testing,
	// in place of GitHub's API.
	if api := os.Getenv("HEYROBOTS_UPDATE_API"); api != "" {
		updater.API, updater.HTTP = strings.TrimRight(api, "/"), &http.Client{Timeout: 10 * time.Minute}
	}

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
		Store:    st,
		Cluster:  node,
		Devices:  devices.New(st, time.Now),
		Vault:    v,
		MCP:      mcpServers,
		Registry: mcpRegistry,
		Files:    filepath.Join(cfg.DataDir, "files"),
		Models:   resolver,
		ACP:      claude,
		Skills:   skillLib,
		Jobs:     jobLib,
		Broker:   api.NewBroker(),
		Log:      log,
		Update:   updater,
		ClearHistory: func(ctx context.Context, forget bool) (store.Cleared, string, error) {
			return clearHistory(ctx, st, workers, cfg.DataDir, forget)
		},
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
	brainRunner := &brain.Runner{Store: st, Log: log, Now: time.Now, Replies: mail.Replies{Google: gg}, Kick: workers.Kick,
		OnChanged: func(threadID string) { srv.ThreadUpdated(threadID); pusher.Kick() }}
	if n, err := brain.ImportNotes(st, filepath.Join(cfg.DataDir, "memory"), time.Now().UTC()); err != nil {
		log.Warn("import agents' notes", "err", err)
	} else if n > 0 {
		log.Info("agents' notes moved into their brains", "facts", n)
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
	serveControl(ctx, cfg.DataDir, node, log)
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
	workersDone := make(chan struct{})
	go func() { workers.Run(ctx); close(workersDone) }()
	go updater.Run(ctx)
	go ob.Run(ctx)
	go pusher.Run(ctx)
	go mailSrc.Run(ctx)
	go calSrc.Run(ctx)
	go schedSrc.Run(ctx)
	go brainRunner.Run(ctx)
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
	if restarting {
		// Tasks cut short pick up again as soon as the new server starts.
		select {
		case <-workersDone:
		case <-shutdownCtx.Done():
		}
		if err := st.Tx(func(tx *store.Tx) error { return tx.InterruptRunning(time.Now().UTC()) }); err != nil {
			log.Warn("mark interrupted tasks", "err", err)
		}
		return errRestart
	}
	return nil
}

// errRestart ends run when an update asks the server to restart.
var errRestart = errors.New("restart")

// version is the server's version, set by the release build
// (-ldflags "-X main.version=0.5.1"); "dev" for a build from source.
var version = "dev"

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

// claudeReady remembers for a few minutes whether Claude Code is signed in
// here, so specialists that prefer it don't ask on every task.
var claudeReady readyCache

type readyCache struct {
	mu sync.Mutex
	at time.Time
	v  bool
}

func (c *readyCache) ok(w *acp.Worker) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < 5*time.Minute {
		return c.v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := w.SignedIn(ctx)
	c.v, c.at = err == nil && v, time.Now()
	return c.v
}

// clearHistory stops running work, keeps a backup of the database, clears
// the chats and tasks, and removes the tasks' folders and shared files.
func clearHistory(ctx context.Context, st *store.Store, workers *executor.Engine, dataDir string, forget bool) (store.Cleared, string, error) {
	workers.Hold(true)
	defer workers.Hold(false)
	workers.StopAll()
	for wait := time.Now().Add(30 * time.Second); workers.Running() > 0; {
		if time.Now().After(wait) {
			return store.Cleared{}, "", errors.New("running work didn't stop in time; try again in a minute")
		}
		select {
		case <-ctx.Done():
			return store.Cleared{}, "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	backup, err := st.Backup("before-clearing-history", time.Now())
	if err != nil {
		return store.Cleared{}, "", fmt.Errorf("the backup failed, so nothing was cleared: %w", err)
	}
	c, err := st.ClearHistory(forget)
	if err != nil {
		return store.Cleared{}, backup, err
	}
	for _, dir := range []string{"workspaces", "files"} {
		if err := os.RemoveAll(filepath.Join(dataDir, dir)); err != nil {
			return c, backup, err
		}
	}
	return c, backup, nil
}
