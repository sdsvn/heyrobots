package skills

import (
	"context"
	"embed"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

// builtin holds the skills heyrobots ships, one folder each.
//
//go:embed builtin
var builtin embed.FS

// Builtin names a shipped skill and the agent that gets it.
type Builtin struct {
	Name  string
	Agent string
}

// Builtins are the skills heyrobots ships, and who gets each.
var Builtins = []Builtin{{Name: "free-data-apis", Agent: "ag_it"}}

// InstallBuiltins adds shipped skills the library has never had, and answers
// those it added. A list in markers records what was installed once, so a
// skill the owner removed stays removed.
func (l Library) InstallBuiltins(ctx context.Context, markers string) ([]Builtin, error) {
	done := map[string]bool{}
	if b, err := os.ReadFile(markers); err == nil {
		for _, n := range strings.Fields(string(b)) {
			done[n] = true
		}
	}
	var added []Builtin
	for _, s := range Builtins {
		if done[s.Name] {
			continue
		}
		files := map[string][]byte{}
		err := fs.WalkDir(builtin, path.Join("builtin", s.Name), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := builtin.ReadFile(p)
			files[strings.TrimPrefix(p, "builtin/"+s.Name+"/")] = b
			return err
		})
		if err != nil {
			return added, err
		}
		if _, err := l.SaveDir(ctx, files, "heyrobots/builtin/"+s.Name); err != nil {
			return added, err
		}
		added = append(added, s)
		done[s.Name] = true
	}
	if len(added) > 0 {
		names := make([]string, 0, len(done))
		for n := range done {
			names = append(names, n)
		}
		slices.Sort(names)
		if err := os.WriteFile(markers, []byte(strings.Join(names, "\n")+"\n"), 0o600); err != nil {
			return added, err
		}
	}
	return added, nil
}
