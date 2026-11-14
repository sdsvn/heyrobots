package cluster

import (
	"context"
	"net"
	"testing"
)

func names(t *testing.T, n *Node) []string {
	t.Helper()
	ms, err := n.Machines()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

// A machine removed by another leaves the team on its side, and a new code
// brings it back.
func TestARemovedMachineLeavesAndCanJoinAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home := newNode(t, "home")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	home.address = ln.Addr().String()
	go home.serve(ctx, ln)
	laptop := newNode(t, "laptop")
	code, _ := home.NewCode()
	if _, err := laptop.Join(ctx, home.address, code); err != nil {
		t.Fatal(err)
	}
	if got := names(t, laptop); len(got) != 2 || got[0] != "laptop" {
		t.Fatalf("laptop sees %v", got)
	}

	if err := home.Remove(laptop.id); err != nil {
		t.Fatal(err)
	}
	if err := laptop.exchange(ctx, member(t, laptop, home.id)); err != nil {
		t.Fatal(err)
	}
	if got := names(t, laptop); len(got) != 1 {
		t.Fatalf("after its removal, laptop still lists %v", got)
	}
	if got := names(t, home); len(got) != 1 {
		t.Fatalf("home still lists %v", got)
	}

	code, _ = home.NewCode()
	if _, err := laptop.Join(ctx, home.address, code); err != nil {
		t.Fatal(err)
	}
	if err := laptop.exchange(ctx, member(t, laptop, home.id)); err != nil {
		t.Fatal(err)
	}
	if a, b := names(t, laptop), names(t, home); len(a) != 2 || len(b) != 2 {
		t.Fatalf("after joining again: laptop %v, home %v", a, b)
	}
	if m, ok := Find(mustMachines(t, home), "LAPTOP"); !ok || !m.Online {
		t.Fatalf("home finds laptop: %+v %v", m, ok)
	}
}

func mustMachines(t *testing.T, n *Node) []Machine {
	ms, err := n.Machines()
	if err != nil {
		t.Fatal(err)
	}
	return ms
}
