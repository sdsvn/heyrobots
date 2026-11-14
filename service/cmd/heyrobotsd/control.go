package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"heyrobots/service/internal/cluster"
	"heyrobots/service/internal/config"
)

// The control socket lets this machine's own commands, such as
// "heyrobotsd machines", ask the running server. It lives in the data
// folder and only this user can open it, so it grants nothing that
// reading the data folder doesn't.
const controlName = "control.sock"

func serveControl(ctx context.Context, dataDir string, node *cluster.Node, log *slog.Logger) {
	path := filepath.Join(dataDir, controlName)
	// This process holds the data lock, so a socket left here is stale.
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Warn("control socket; machine commands won't reach this server", "err", err)
		return
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		log.Warn("control socket", "err", err)
		return
	}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /machines", func(w http.ResponseWriter, r *http.Request) {
		ms, err := node.Machines()
		reply(w, ms, err)
	})
	mux.HandleFunc("POST /machines/code", func(w http.ResponseWriter, r *http.Request) {
		if node.Address() == "" {
			reply(w, nil, errors.New("other machines can't reach this one: set HEYROBOTS_CLUSTER_LISTEN in the service's settings, or run \"heyrobotsd machines join\" on this machine instead"))
			return
		}
		code, exp := node.NewCode()
		reply(w, map[string]any{"code": code, "address": node.Address(), "expires": exp}, nil)
	})
	mux.HandleFunc("POST /machines/join", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Address, Code string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			reply(w, nil, err)
			return
		}
		jctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		m, err := node.Join(jctx, req.Address, req.Code)
		if err == nil {
			log.Info("joined machine", "member", m.ID, "name", m.Name, "from", "command line")
		}
		reply(w, map[string]string{"name": m.Name, "id": m.ID}, err)
	})
	mux.HandleFunc("DELETE /machines/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{}, node.Remove(r.PathValue("id")))
	})
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		hs.Close()
		os.Remove(path)
	}()
	go hs.Serve(ln)
}

// control asks the running server; out receives the JSON answer.
func control(method, path string, body, out any) error {
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}
	sock := filepath.Join(cfg.DataDir, controlName)
	c := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, "http://heyrobotsd"+path, rd)
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("no heyrobots server is running with %s (start it, or set HEYROBOTS_DATA_DIR to the service's data folder)", cfg.DataDir)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var e struct{ Error string }
		json.NewDecoder(res.Body).Decode(&e)
		return errors.New(e.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func machinesCommand(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list":
		var ms []cluster.Machine
		if err := control("GET", "/machines", nil, &ms); err != nil {
			return err
		}
		online := 0
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATE\tADDRESS\tAGENTS\tID")
		for _, m := range ms {
			state := "online"
			switch {
			case m.Self:
				state = "this machine"
			case !m.Online && m.LastSeen.IsZero():
				state = "never seen"
			case !m.Online:
				state = "last seen " + ago(time.Since(m.LastSeen))
			}
			if m.Online {
				online++
			}
			addr := m.Address
			if addr == "" {
				addr = "dials out only"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", m.Name, state, addr, m.Agents, m.ID)
		}
		w.Flush()
		if len(ms) == 1 {
			fmt.Println("\nOnly this machine. To add one: \"heyrobotsd machines code\" here, then \"heyrobotsd machines join\" there.")
		}
		return nil
	case "code":
		var c struct {
			Code, Address string
			Expires       time.Time
		}
		if err := control("POST", "/machines/code", nil, &c); err != nil {
			return err
		}
		fmt.Printf("On the other machine, run:\n\n  heyrobotsd machines join %s %s\n\nor in its app: Settings → Your machines → Join a machine. Works once, until %s.\n", c.Address, c.Code, c.Expires.Local().Format("15:04"))
		return nil
	case "join":
		if len(args) != 3 {
			return errors.New("usage: heyrobotsd machines join <address> <code>")
		}
		var m struct{ Name string }
		if err := control("POST", "/machines/join", map[string]string{"Address": args[1], "Code": args[2]}, &m); err != nil {
			return fmt.Errorf("couldn't join that machine: %w", err)
		}
		fmt.Printf("Joined %s. Its agents show in the app within a few seconds.\n", m.Name)
		return nil
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: heyrobotsd machines remove <name or id>")
		}
		var ms []cluster.Machine
		if err := control("GET", "/machines", nil, &ms); err != nil {
			return err
		}
		m, ok := cluster.Find(ms, args[1])
		if !ok {
			return fmt.Errorf("no single machine is called %q; \"heyrobotsd machines\" lists them with their ids", args[1])
		}
		if err := control("DELETE", "/machines/"+m.ID, nil, nil); err != nil {
			return err
		}
		fmt.Printf("Removed %s from the team; the other machines hear of it on their next contact.\n", m.Name)
		return nil
	}
	return fmt.Errorf("unknown machines command %q; see \"heyrobotsd help\"", sub)
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
