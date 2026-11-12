// Package acp runs an agent's work in the owner's own Claude Code, through
// the claude-agent-acp adapter, over the Agent Client Protocol. The
// unmodified claude runs under heyrobots' own config folder, which the
// owner signs into once; heyrobots never sees a token.
package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// Adapter is the adapter npx starts when none is on PATH.
const Adapter = "@agentclientprotocol/claude-agent-acp@0.106.0"

// ErrNotSignedIn says heyrobots' config folder holds no Claude login.
var ErrNotSignedIn = errors.New("Claude Code isn't signed in for heyrobots")

// Worker starts Claude Code sessions.
type Worker struct {
	// Command starts the adapter. Empty finds claude-agent-acp on PATH,
	// else runs it with npx.
	Command []string
	// ConfigDir is Claude Code's config folder. Empty uses the owner's
	// own sign-in (~/.claude).
	ConfigDir string
	// Model picks Claude Code's model, such as claude-sonnet-5-5, so chat
	// turns stay quick; empty keeps Claude Code's own default. Other ACP
	// agents take theirs on their command line.
	Model string
	Log   *slog.Logger

	mu      sync.Mutex
	spare   *proc
	warming bool
}

// LoginCommand is what the owner runs once in a terminal.
func (w *Worker) LoginCommand() string {
	if w.ConfigDir == "" {
		return "claude auth login"
	}
	return "CLAUDE_CONFIG_DIR=" + w.ConfigDir + " claude auth login"
}

// env drops the server's own ANTHROPIC_*/CLAUDE* variables: a key or
// token inherited there (say, from a Claude Code session that started
// the server) would be used instead of the owner's sign-in.
func (w *Worker) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "ANTHROPIC_") || strings.HasPrefix(kv, "CLAUDE") {
			continue
		}
		env = append(env, kv)
	}
	if w.ConfigDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+w.ConfigDir)
	}
	if w.Model != "" {
		env = append(env, "ANTHROPIC_MODEL="+w.Model)
	}
	// The adapter would otherwise spawn the SDK's own copy of claude,
	// which doesn't hold the owner's sign-in.
	if bin, err := exec.LookPath("claude"); err == nil {
		env = append(env, "CLAUDE_CODE_EXECUTABLE="+bin)
	}
	return env
}

// SignedIn asks claude whether heyrobots' config folder holds a login.
// Another ACP agent (HEYROBOTS_ACP_COMMAND) handles its own sign-in and
// answers "auth required" itself.
func (w *Worker) SignedIn(ctx context.Context) (bool, error) {
	if len(w.Command) > 0 {
		return true, nil
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		return false, errors.New("claude isn't installed on this machine")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status", "--json")
	cmd.Env = w.env()
	// It exits 0 signed in or not; the answer says which.
	out, _ := cmd.Output()
	var st struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if json.Unmarshal(out, &st) != nil {
		return false, errors.New("claude auth status gave no answer")
	}
	return st.LoggedIn, nil
}

func (w *Worker) argv() ([]string, error) {
	if len(w.Command) > 0 {
		return w.Command, nil
	}
	if p, err := exec.LookPath("claude-agent-acp"); err == nil {
		return []string{p}, nil
	}
	if p, err := exec.LookPath("npx"); err == nil {
		return []string{p, "-y", Adapter}, nil
	}
	return nil, errors.New("install Node 22, or the claude-agent-acp adapter")
}

// Session is one ACP session with a running adapter.
type Session struct {
	// ID is the session's id, kept to load it again later.
	ID string
	// Loaded says an earlier session was loaded instead of a new one.
	Loaded bool

	p    *proc
	stop func() bool
}

// proc is one running adapter, initialized and waiting for a session.
type proc struct {
	cmd    *exec.Cmd
	conn   *acp.ClientSideConnection
	stderr *lastLine
	fwd    *forward
	init   acp.InitializeResponse
	exited chan struct{}
}

func (p *proc) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	<-p.exited
}

func (p *proc) alive() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// spawn starts an adapter and initializes it. It isn't tied to a task, so a
// spare can wait for the next one.
func (w *Worker) spawn(ctx context.Context) (*proc, error) {
	argv, err := w.argv()
	if err != nil {
		return nil, err
	}
	p := &proc{stderr: &lastLine{log: w.Log}, fwd: &forward{c: BaseClient{}}, exited: make(chan struct{})}
	p.cmd = exec.Command(argv[0], argv[1:]...)
	p.cmd.Dir = os.TempDir()
	p.cmd.Env = w.env()
	p.cmd.Stderr = p.stderr
	p.cmd.WaitDelay = 2 * time.Second
	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := p.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the adapter: %w", err)
	}
	go func() {
		_ = p.cmd.Wait()
		close(p.exited)
	}()
	p.conn = acp.NewClientSideConnection(p.fwd, stdin, stdout)
	if w.Log != nil {
		p.conn.SetLogger(slog.New(quiet{w.Log.Handler()}))
	}
	ictx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	p.init, err = p.conn.Initialize(ictx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo:      &acp.Implementation{Name: "heyrobots", Version: "0.1"},
	})
	if err != nil {
		p.kill()
		s := &Session{p: p}
		return nil, s.wrap(err)
	}
	return p, nil
}

// Warm starts a spare adapter in the background, so the next task doesn't
// wait for one to start. One spare at most.
func (w *Worker) Warm() {
	w.mu.Lock()
	if w.warming || (w.spare != nil && w.spare.alive()) {
		w.mu.Unlock()
		return
	}
	w.warming = true
	w.mu.Unlock()
	go func() {
		p, err := w.spawn(context.Background())
		w.mu.Lock()
		defer w.mu.Unlock()
		w.warming = false
		if err != nil {
			if w.Log != nil {
				w.Log.Debug("acp: no spare adapter", "err", err)
			}
			return
		}
		w.spare = p
	}()
}

// take returns the spare adapter when one is ready, else starts one.
func (w *Worker) take(ctx context.Context) (*proc, error) {
	w.mu.Lock()
	p := w.spare
	w.spare = nil
	w.mu.Unlock()
	if p != nil && p.alive() {
		return p, nil
	}
	return w.spawn(ctx)
}

// Start opens a session in dir with the MCP servers, on a spare adapter
// when one is ready. When loadID names an earlier session and the agent can
// load sessions, it loads that one; Loaded says whether it did. Each
// session has its own adapter, ended by Close or when ctx ends.
func (w *Worker) Start(ctx context.Context, client acp.Client, dir string, servers []acp.McpServer, loadID string) (*Session, error) {
	p, err := w.take(ctx)
	defer w.Warm()
	if err != nil {
		return nil, err
	}
	p.fwd.set(client)
	s := &Session{p: p, stop: context.AfterFunc(ctx, func() { p.kill() })}
	if servers == nil {
		servers = []acp.McpServer{}
	}
	if loadID != "" && p.init.AgentCapabilities.LoadSession {
		if _, err := p.conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: acp.SessionId(loadID), Cwd: dir, McpServers: servers}); err == nil {
			s.ID, s.Loaded = loadID, true
			return s, nil
		} else if w.Log != nil {
			w.Log.Info("acp session not loaded, starting a new one", "err", err)
		}
	}
	ns, err := p.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: dir, McpServers: servers})
	if err != nil {
		s.Close()
		return nil, s.wrap(err)
	}
	s.ID = string(ns.SessionId)
	return s, nil
}

// forward passes the agent's requests to the client of the session that
// took the adapter.
type forward struct {
	mu sync.Mutex
	c  acp.Client
}

func (f *forward) set(c acp.Client) {
	f.mu.Lock()
	f.c = c
	f.mu.Unlock()
}

func (f *forward) cur() acp.Client {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.c
}

func (f *forward) ReadTextFile(ctx context.Context, p acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return f.cur().ReadTextFile(ctx, p)
}

func (f *forward) WriteTextFile(ctx context.Context, p acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return f.cur().WriteTextFile(ctx, p)
}

func (f *forward) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return f.cur().RequestPermission(ctx, p)
}

func (f *forward) SessionUpdate(ctx context.Context, p acp.SessionNotification) error {
	return f.cur().SessionUpdate(ctx, p)
}

func (f *forward) CreateTerminal(ctx context.Context, p acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return f.cur().CreateTerminal(ctx, p)
}

func (f *forward) KillTerminal(ctx context.Context, p acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return f.cur().KillTerminal(ctx, p)
}

func (f *forward) TerminalOutput(ctx context.Context, p acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return f.cur().TerminalOutput(ctx, p)
}

func (f *forward) ReleaseTerminal(ctx context.Context, p acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return f.cur().ReleaseTerminal(ctx, p)
}

func (f *forward) WaitForTerminalExit(ctx context.Context, p acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return f.cur().WaitForTerminalExit(ctx, p)
}

// Prompt sends one user turn and waits for the turn to end.
func (s *Session) Prompt(ctx context.Context, text string) (acp.StopReason, error) {
	resp, err := s.p.conn.Prompt(ctx, acp.PromptRequest{SessionId: acp.SessionId(s.ID), Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
	if err != nil {
		return "", s.wrap(err)
	}
	return resp.StopReason, nil
}

// Close ends the session's adapter.
func (s *Session) Close() {
	if s.stop != nil {
		s.stop()
	}
	s.p.kill()
}

func (s *Session) wrap(err error) error {
	if IsAuthRequired(err) {
		return ErrNotSignedIn
	}
	if last := s.p.stderr.String(); last != "" {
		return fmt.Errorf("%w (%s)", err, last)
	}
	return err
}

// IsAuthRequired reports the protocol's "authentication required" answer.
func IsAuthRequired(err error) bool {
	var re *acp.RequestError
	return errors.As(err, &re) && re.Code == -32000
}

// Check opens a throwaway session and asks for one word, so the choice is
// tried before it is saved.
func (w *Worker) Check(ctx context.Context) error {
	ok, err := w.SignedIn(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: run %s in a terminal first", ErrNotSignedIn, w.LoginCommand())
	}
	text, err := w.Ask(ctx, "Reply with the single word: pong")
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(text), "pong") {
		return fmt.Errorf("your agent answered %q instead of pong", text)
	}
	return nil
}

// Ask opens a throwaway session in an empty folder, with no tools of
// heyrobots', sends one prompt and returns the agent's answer.
func (w *Worker) Ask(ctx context.Context, prompt string) (string, error) {
	dir, err := os.MkdirTemp("", "heyrobots-acp-ask")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	c := &textClient{}
	s, err := w.Start(ctx, c, dir, nil, "")
	if err != nil {
		return "", err
	}
	defer s.Close()
	if _, err := s.Prompt(ctx, prompt); err != nil {
		return "", err
	}
	return c.Text(), nil
}

// BaseClient refuses everything an agent may ask of a client: no file
// access, no terminals, no permissions. Embed it and override what a
// client allows.
type BaseClient struct{}

var _ acp.Client = (*BaseClient)(nil)

// RequestPermission refuses.
func (BaseClient) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return Answer(p, false), nil
}

// Answer picks the first option that allows once, or refuses once.
func Answer(p acp.RequestPermissionRequest, allow bool) acp.RequestPermissionResponse {
	want := acp.PermissionOptionKindRejectOnce
	if allow {
		want = acp.PermissionOptionKindAllowOnce
	}
	for _, o := range p.Options {
		if o.Kind == want {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(o.OptionId)}
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}
}

// SessionUpdate ignores updates.
func (BaseClient) SessionUpdate(context.Context, acp.SessionNotification) error { return nil }

var errNoFS = errors.New("heyrobots gives no file or terminal access over ACP")

// ReadTextFile refuses.
func (BaseClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errNoFS
}

// WriteTextFile refuses.
func (BaseClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errNoFS
}

// CreateTerminal refuses.
func (BaseClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errNoFS
}

// KillTerminal refuses.
func (BaseClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errNoFS
}

// ReleaseTerminal refuses.
func (BaseClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errNoFS
}

// TerminalOutput refuses.
func (BaseClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errNoFS
}

// WaitForTerminalExit refuses.
func (BaseClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errNoFS
}

// textClient collects what the agent says.
type textClient struct {
	BaseClient
	mu  sync.Mutex
	buf strings.Builder
}

func (c *textClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	if ch := n.Update.AgentMessageChunk; ch != nil && ch.Content.Text != nil {
		c.mu.Lock()
		c.buf.WriteString(ch.Content.Text.Text)
		c.mu.Unlock()
	}
	return nil
}

func (c *textClient) Text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.buf.String())
}

// lastLine keeps the adapter's last line of stderr, for error messages.
type lastLine struct {
	log  *slog.Logger
	mu   sync.Mutex
	last string
}

func (l *lastLine) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range bytes.Split(bytes.TrimSpace(p), []byte("\n")) {
		if s := strings.TrimSpace(string(line)); s != "" {
			l.last = s
			if l.log != nil {
				l.log.Debug("claude-agent-acp", "line", s)
			}
		}
	}
	return len(p), nil
}

func (l *lastLine) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.last) > 200 {
		return l.last[:200] + "…"
	}
	return l.last
}

// quiet logs the protocol library's routine messages at debug level: a
// notification heyrobots has no use for, such as Claude Code's
// auth/status_update, and an adapter ending after its task. Real errors
// still reach the log.
type quiet struct{ slog.Handler }

func (q quiet) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "connection closed" || r.Message == "failed to handle notification" && strings.Contains(fmt.Sprint(attr(r, "err")), "Method not found") {
		r.Level = slog.LevelDebug
		if !q.Handler.Enabled(ctx, r.Level) {
			return nil
		}
	}
	return q.Handler.Handle(ctx, r)
}

func (q quiet) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelDebug && q.Handler.Enabled(ctx, slog.LevelDebug) || q.Handler.Enabled(ctx, l)
}

func (q quiet) WithAttrs(as []slog.Attr) slog.Handler { return quiet{q.Handler.WithAttrs(as)} }

func (q quiet) WithGroup(n string) slog.Handler { return quiet{q.Handler.WithGroup(n)} }

func attr(r slog.Record, key string) any {
	var v any
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value.Any()
			return false
		}
		return true
	})
	return v
}
