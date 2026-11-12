// Package sandbox runs commands for toolsets that need them, such as code
// and data, away from the owner's machine. With Docker or OrbStack it uses
// a throwaway container per run that sees only the Ticket's workspace and
// reaches the network only through the egress proxy. Without a container
// runtime it falls back to the prototype's limits: the workspace as the
// working folder, a blocked-command list, no shell and a scrubbed
// environment. That fallback can't stop a program from ignoring the proxy,
// so it has no real network limit, and the server says so.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Mode names how commands are isolated.
type Mode string

const (
	// ModeContainer runs each command in a throwaway container.
	ModeContainer Mode = "container"
	// ModeLimited runs commands on the host with workspace limits only.
	ModeLimited Mode = "limited"
)

// Spec is one command to run.
type Spec struct {
	// Workspace is the Ticket's folder. It is the only folder the command
	// can write to.
	Workspace string
	// Argv is the program and its arguments. It runs without a shell.
	Argv []string
	// ProxyURL, when set, is the egress proxy for network access. When
	// empty the command gets no network.
	ProxyURL string
	Timeout  time.Duration
}

// Result is what a command produced. Output is capped.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	TimedOut bool
}

// Runner runs commands.
type Runner interface {
	Mode() Mode
	Run(ctx context.Context, s Spec) (Result, error)
}

// MaxOutput caps each of stdout and stderr.
const MaxOutput = 64 << 10

// DefaultTimeout applies when a Spec gives none.
const DefaultTimeout = 5 * time.Minute

// ErrBlocked is returned for a command the limits refuse.
var ErrBlocked = errors.New("command blocked")

// Detect returns a container runner when Docker (or OrbStack, which
// provides the docker command) answers, and the limited runner otherwise.
func Detect(ctx context.Context, image string) Runner {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if path, err := exec.LookPath("docker"); err == nil {
		if exec.CommandContext(ctx, path, "info", "--format", "{{.ServerVersion}}").Run() == nil {
			return &Docker{Binary: path, Image: image}
		}
	}
	return &Limited{}
}

// Docker runs each command in a new container.
type Docker struct {
	Binary string
	Image  string
	// Network is the Docker network that can reach only the proxy. Without
	// a proxy the container gets no network at all.
	Network string
}

// Mode reports ModeContainer.
func (d *Docker) Mode() Mode { return ModeContainer }

// Args builds the docker command line for a Spec.
func (d *Docker) Args(s Spec) []string {
	network := "none"
	if s.ProxyURL != "" && d.Network != "" {
		network = d.Network
	}
	args := []string{
		// The server never downloads images on its own; the owner pulls the
		// sandbox image once during setup.
		"run", "--rm", "--init", "--pull", "never",
		"--network", network,
		"--read-only",
		"--tmpfs", "/tmp:rw,size=256m",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "256",
		"--memory", "1g",
		"--cpus", "1",
		"--user", "1000:1000",
		"--mount", "type=bind,source=" + s.Workspace + ",target=/work",
		"--workdir", "/work",
		"--env", "HOME=/work",
	}
	if network != "none" {
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			args = append(args, "--env", k+"="+s.ProxyURL)
		}
	}
	args = append(args, d.Image)
	return append(args, s.Argv...)
}

// Run runs a command in a throwaway container.
func (d *Docker) Run(ctx context.Context, s Spec) (Result, error) {
	if err := checkSpec(s); err != nil {
		return Result{}, err
	}
	return run(ctx, s, d.Binary, d.Args(s), s.Workspace, os.Environ())
}

// Limited runs commands on the host inside the workspace.
type Limited struct{}

// Mode reports ModeLimited.
func (l *Limited) Mode() Mode { return ModeLimited }

// blockedCommands can change the machine, reach other machines, read the
// owner's secrets, or start a shell that would get around this list.
var blockedCommands = []string{
	"sudo", "su", "doas", "pkexec",
	"sh", "bash", "zsh", "fish", "dash", "ksh", "csh", "tcsh", "env", "xargs", "nohup", "exec",
	"ssh", "scp", "sftp", "rsync", "nc", "ncat", "netcat", "telnet", "socat", "ftp",
	"osascript", "launchctl", "security", "defaults", "crontab", "at", "systemctl", "service",
	"shutdown", "reboot", "halt", "mount", "umount", "diskutil", "docker", "podman", "kill", "killall", "pkill",
	"chown", "chmod", "chflags", "dd", "mkfs",
}

// Blocked reports why the limited runner refuses a command, or "".
func Blocked(argv []string) string {
	if len(argv) == 0 {
		return "empty command"
	}
	name := filepath.Base(argv[0])
	if slices.Contains(blockedCommands, name) {
		return name + " is not allowed"
	}
	if name == "git" && slices.Contains(argv, "push") && (slices.Contains(argv, "--force") || slices.Contains(argv, "-f")) {
		return "force push is not allowed"
	}
	return ""
}

// Run runs a command on the host with the fallback limits.
func (l *Limited) Run(ctx context.Context, s Spec) (Result, error) {
	if err := checkSpec(s); err != nil {
		return Result{}, err
	}
	if why := Blocked(s.Argv); why != "" {
		return Result{}, fmt.Errorf("%w: %s", ErrBlocked, why)
	}
	tmp := filepath.Join(s.Workspace, ".tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return Result{}, err
	}
	// Start from an empty environment so the owner's tokens and keys never
	// reach the command.
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin", "HOME=" + s.Workspace, "TMPDIR=" + tmp, "LANG=C.UTF-8"}
	if s.ProxyURL != "" {
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env = append(env, k+"="+s.ProxyURL)
		}
	} else {
		// Point programs that honour proxy settings at nothing.
		env = append(env, "HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=")
	}
	path, err := exec.LookPath(s.Argv[0])
	if err != nil {
		return Result{}, err
	}
	return run(ctx, s, path, s.Argv[1:], s.Workspace, env)
}

func checkSpec(s Spec) error {
	if len(s.Argv) == 0 {
		return fmt.Errorf("%w: empty command", ErrBlocked)
	}
	if !filepath.IsAbs(s.Workspace) {
		return errors.New("sandbox: workspace must be an absolute path")
	}
	info, err := os.Stat(s.Workspace)
	if err != nil || !info.IsDir() {
		return errors.New("sandbox: workspace must be an existing folder")
	}
	return nil
}

func run(ctx context.Context, s Spec, bin string, args []string, dir string, env []string) (Result, error) {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir, cmd.Env = dir, env
	var out, errb capped
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String(), TimedOut: ctx.Err() == context.DeadlineExceeded}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		res.ExitCode = exit.ExitCode()
	case res.TimedOut:
		res.ExitCode = -1
	default:
		return res, err
	}
	return res, nil
}

// capped keeps at most MaxOutput bytes. The buffer is a named field, not
// embedded: an embedded bytes.Buffer would expose ReadFrom, and io.Copy
// would use it and skip the cap.
type capped struct {
	buf bytes.Buffer
	cut bool
}

func (c *capped) Write(p []byte) (int, error) {
	if c.cut {
		return len(p), nil
	}
	room := MaxOutput - c.buf.Len()
	if len(p) > room {
		c.buf.Write(p[:room])
		c.buf.WriteString("\n[output cut]")
		c.cut = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

// String returns the captured output with surrounding space trimmed.
func (c *capped) String() string { return strings.TrimSpace(c.buf.String()) }
