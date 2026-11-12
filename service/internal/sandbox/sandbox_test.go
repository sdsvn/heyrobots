package sandbox

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDockerArgsIsolateTheContainer(t *testing.T) {
	d := &Docker{Binary: "docker", Image: "heyrobots/sandbox:1", Network: "heyrobots-egress"}
	args := strings.Join(d.Args(Spec{Workspace: "/data/ws/tk_1", Argv: []string{"python3", "x.py"}}), " ")
	for _, want := range []string{"--network none", "--read-only", "--cap-drop ALL", "no-new-privileges", "--pids-limit", "--memory 1g", "--user 1000:1000",
		"source=/data/ws/tk_1,target=/work", "heyrobots/sandbox:1 python3 x.py"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	if strings.Contains(args, "PROXY") {
		t.Error("a run without a proxy must get no network and no proxy settings")
	}
	withProxy := strings.Join(d.Args(Spec{Workspace: "/w", Argv: []string{"curl", "x"}, ProxyURL: "http://host.docker.internal:7399"}), " ")
	if !strings.Contains(withProxy, "--network heyrobots-egress") || !strings.Contains(withProxy, "HTTPS_PROXY=http://host.docker.internal:7399") {
		t.Errorf("proxy run: %s", withProxy)
	}
	// Only the workspace is mounted.
	if n := strings.Count(args, "--mount"); n != 1 {
		t.Errorf("%d mounts, want only the workspace", n)
	}
}

func TestLimitedBlocksDangerousCommands(t *testing.T) {
	for _, argv := range [][]string{{"sudo", "ls"}, {"/bin/sh", "-c", "curl evil"}, {"bash"}, {"security", "find-generic-password"},
		{"ssh", "host"}, {"env", "sh"}, {"git", "push", "--force"}, {"launchctl", "list"}, {}} {
		if Blocked(argv) == "" {
			t.Errorf("%v must be blocked", argv)
		}
	}
	for _, argv := range [][]string{{"echo", "hi"}, {"python3", "a.py"}, {"git", "status"}} {
		if why := Blocked(argv); why != "" {
			t.Errorf("%v blocked: %s", argv, why)
		}
	}
}

func TestLimitedRunsInTheWorkspaceWithAScrubbedEnvironment(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("GOOGLE_API_KEY", "must-not-leak")
	l := &Limited{}
	res, err := l.Run(context.Background(), Spec{Workspace: ws, Argv: []string{"/usr/bin/printenv"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Stdout, "must-not-leak") {
		t.Fatal("the owner's environment reached the command")
	}
	if !strings.Contains(res.Stdout, "HOME="+ws) {
		t.Fatalf("HOME is not the workspace: %s", res.Stdout)
	}
	res, err = l.Run(context.Background(), Spec{Workspace: ws, Argv: []string{"pwd"}})
	if err != nil {
		t.Fatal(err)
	}
	real, _ := os.Readlink(ws)
	if res.Stdout != ws && !strings.HasSuffix(res.Stdout, strings.TrimPrefix(ws, "/private")) && res.Stdout != real {
		t.Fatalf("pwd = %q, want the workspace %q", res.Stdout, ws)
	}
	if _, err := l.Run(context.Background(), Spec{Workspace: ws, Argv: []string{"sudo", "id"}}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("sudo: %v", err)
	}
}

func TestTimeoutStopsTheCommand(t *testing.T) {
	res, err := (&Limited{}).Run(context.Background(), Spec{Workspace: t.TempDir(), Argv: []string{"sleep", "5"}, Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("result = %+v, want timed out", res)
	}
}

func TestOutputIsCapped(t *testing.T) {
	res, err := (&Limited{}).Run(context.Background(), Spec{Workspace: t.TempDir(), Argv: []string{"head", "-c", "200000", "/dev/zero"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) > MaxOutput+32 || !strings.Contains(res.Stdout, "[output cut]") {
		t.Fatalf("stdout is %d bytes", len(res.Stdout))
	}
}

func TestDetectFallsBackWithoutDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if r := Detect(context.Background(), "img"); r.Mode() != ModeLimited || !slices.Contains([]Mode{ModeLimited}, r.Mode()) {
		t.Fatalf("mode = %s", r.Mode())
	}
}

// TestDockerContainerIsIsolated runs a real container when Docker and the
// local alpine image are available. It is skipped otherwise.
func TestDockerContainerIsIsolated(t *testing.T) {
	const image = "alpine:3.24"
	r := Detect(context.Background(), image)
	d, ok := r.(*Docker)
	if !ok {
		t.Skip("docker is not available")
	}
	if err := runQuiet(d.Binary, "image", "inspect", image); err != nil {
		t.Skipf("%s is not present locally", image)
	}
	ws := t.TempDir()
	os.WriteFile(ws+"/in.txt", []byte("workspace file"), 0o644)
	os.Chmod(ws, 0o777)

	run := func(argv ...string) Result {
		t.Helper()
		res, err := d.Run(context.Background(), Spec{Workspace: ws, Argv: argv, Timeout: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := run("cat", "/work/in.txt"); res.Stdout != "workspace file" {
		t.Fatalf("workspace not mounted: %+v", res)
	}
	if res := run("sh", "-c", "echo out > /work/out.txt && cat /work/out.txt"); res.Stdout != "out" {
		t.Fatalf("workspace not writable: %+v", res)
	}
	if res := run("sh", "-c", "touch /etc/x"); res.ExitCode == 0 {
		t.Fatal("the container's own filesystem must be read-only")
	}
	if res := run("id", "-u"); res.Stdout != "1000" {
		t.Fatalf("runs as uid %q, want 1000", res.Stdout)
	}
	if res := run("sh", "-c", "wget -q -T 3 -O- http://1.1.1.1 || echo no-network"); !strings.Contains(res.Stdout+res.Stderr, "no-network") && res.ExitCode == 0 {
		t.Fatalf("a container without a proxy reached the network: %+v", res)
	}
	if res := run("sh", "-c", "ls /Users 2>&1 || true"); strings.Contains(res.Stdout, os.Getenv("USER")) && os.Getenv("USER") != "" {
		t.Fatal("the owner's home is visible in the container")
	}
}

func runQuiet(bin string, args ...string) error {
	return execCommand(bin, args...).Run()
}
