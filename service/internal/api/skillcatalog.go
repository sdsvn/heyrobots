package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"heyrobots/service/internal/api/gen"
	"heyrobots/service/internal/catalog"
	"heyrobots/service/internal/skills"
)

// Limits on a skill fetched from GitHub.
const (
	maxSkillFolder = 1 << 20
	maxSkillFiles  = 200
)

var (
	githubSource = regexp.MustCompile(`^(?:https?://github\.com/)?([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)(?:/tree/([^/]+))?(?:/(.*?))?/?$`)
	commitSHA    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ghSource is a folder in a GitHub repository.
type ghSource struct{ owner, repo, ref, dir string }

func (g ghSource) String() string { return strings.TrimSuffix(g.owner+"/"+g.repo+"/"+g.dir, "/") }

// parseSource reads owner/repo/path or a github.com folder link.
func parseSource(s string) (ghSource, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "https://github.com/") && (strings.Count(s, "/") < 2 || strings.ContainsAny(s, " \t")) {
		return ghSource{}, false
	}
	m := githubSource.FindStringSubmatch(s)
	if m == nil || strings.Contains(m[4], "..") {
		return ghSource{}, false
	}
	return ghSource{owner: m[1], repo: m[2], ref: m[3], dir: strings.Trim(m[4], "/")}, true
}

var skillHTTP = &http.Client{Timeout: 20 * time.Second}

func (s *Server) ghGet(ctx context.Context, u, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := skillHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("the skill is larger than 1 MB")
	}
	return b, err
}

// fetchSkill reads a skill folder at one commit: the commit, its tree in
// one call, then the files from raw.githubusercontent.com. With all false
// it reads only SKILL.md. Links and submodules are refused.
func (s *Server) fetchSkill(ctx context.Context, src ghSource, all bool) (map[string][]byte, string, error) {
	api := fmt.Sprintf("%s/repos/%s/%s", s.opts.Probe.GitHubAPI, url.PathEscape(src.owner), url.PathEscape(src.repo))
	ref := src.ref
	if ref == "" {
		ref = "HEAD"
	}
	b, err := s.ghGet(ctx, api+"/commits/"+url.PathEscape(ref), "application/vnd.github.sha", 100)
	if err != nil {
		return nil, "", fmt.Errorf("can't find %s: %w", src, err)
	}
	sha := strings.TrimSpace(string(b))
	if !commitSHA.MatchString(sha) {
		return nil, "", errors.New("GitHub didn't answer with a commit")
	}
	b, err = s.ghGet(ctx, api+"/git/trees/"+sha+"?recursive=1", "application/vnd.github+json", 8<<20)
	if err != nil {
		return nil, "", fmt.Errorf("can't read %s: %w", src, err)
	}
	var tree struct {
		Tree []struct {
			Path, Mode, Type string
			Size             int64
		}
	}
	if err := json.Unmarshal(b, &tree); err != nil {
		return nil, "", err
	}
	prefix := ""
	if src.dir != "" {
		prefix = src.dir + "/"
	}
	files := map[string][]byte{}
	var total int64
	for _, e := range tree.Tree {
		rel, ok := strings.CutPrefix(e.Path, prefix)
		if !ok || e.Type == "tree" {
			continue
		}
		if e.Type != "blob" || e.Mode == "120000" {
			return nil, "", fmt.Errorf("the skill has a link or submodule (%s); only plain files can be added", rel)
		}
		if total += e.Size; total > maxSkillFolder {
			return nil, "", errors.New("the skill is larger than 1 MB")
		}
		if len(files) >= maxSkillFiles {
			return nil, "", fmt.Errorf("the skill has more than %d files", maxSkillFiles)
		}
		if !all && rel != "SKILL.md" {
			continue
		}
		raw := fmt.Sprintf("%s/%s/%s/%s/%s", s.opts.Probe.GitHubRaw, url.PathEscape(src.owner), url.PathEscape(src.repo), sha, (&url.URL{Path: e.Path}).EscapedPath())
		if files[rel], err = s.ghGet(ctx, raw, "", maxSkillFolder); err != nil {
			return nil, "", fmt.Errorf("can't read %s: %w", rel, err)
		}
	}
	if files["SKILL.md"] == nil {
		return nil, "", fmt.Errorf("%s has no SKILL.md", src)
	}
	return files, sha, nil
}

// SearchSkills lists installed and curated skills matching q, or the one
// skill a GitHub link points at. Owner devices only.
func (s *Server) SearchSkills(w http.ResponseWriter, r *http.Request, p gen.SearchSkillsParams) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	q := ""
	if p.Q != nil {
		q = strings.TrimSpace(*p.Q)
	}
	installed := map[string]bool{}
	out := gen.SkillCatalog{Skills: []gen.CatalogSkill{}}
	if s.opts.Skills != nil {
		lib, err := s.opts.Skills.List(r.Context())
		if err != nil {
			s.internal(w, "list skills", err)
			return
		}
		for _, sk := range lib {
			installed[sk.Name] = true
			if matches(q, sk.Name, sk.Description) {
				src, _ := os.ReadFile(filepath.Join(s.opts.Skills.Dir, sk.Name, skills.SourceFile))
				repo, _, _ := strings.Cut(strings.TrimSpace(string(src)), "@")
				out.Skills = append(out.Skills, gen.CatalogSkill{Name: sk.Name, Description: sk.Description, Installed: true, Source: repo})
			}
		}
	}
	if src, ok := parseSource(q); ok {
		files, _, err := s.fetchSkill(r.Context(), src, false)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		name, desc, err := skills.Frontmatter(files["SKILL.md"])
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		out.Skills = []gen.CatalogSkill{{Source: src.String(), Name: name, Description: desc, Installed: installed[name]}}
		writeJSON(w, http.StatusOK, out)
		return
	}
	for _, c := range catalog.Skills() {
		if installed[c.Name] || !matches(q, c.Name, c.Description) {
			continue
		}
		v := gen.CatalogSkill{Source: c.Source, Name: c.Name, Description: c.Description}
		if c.SuggestedFor != "" {
			v.SuggestedFor = &c.SuggestedFor
		}
		out.Skills = append(out.Skills, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func matches(q string, fields ...string) bool {
	q = strings.ToLower(q)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// AddSkill fetches a skill folder from GitHub and installs it, pinned to
// the commit it was read at. Owner devices only.
func (s *Server) AddSkill(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireOwner(w, r); !ok {
		return
	}
	if s.opts.Skills == nil {
		writeError(w, http.StatusBadRequest, "there is no skills library on this server")
		return
	}
	var req gen.AddSkillRequest
	if !decode(w, r, &req) {
		return
	}
	src, ok := parseSource(req.Source)
	if !ok {
		writeError(w, http.StatusBadRequest, "give a GitHub folder as owner/repo/path or a github.com link")
		return
	}
	files, sha, err := s.fetchSkill(r.Context(), src, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := s.opts.Skills.SaveDir(r.Context(), files, src.String()+"@"+sha)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lib, err := s.opts.Skills.List(r.Context())
	if err != nil {
		s.internal(w, "list skills", err)
		return
	}
	for _, sk := range lib {
		if sk.Name == name {
			s.opts.Log.Info("skill added", "skill", name, "source", src.String(), "commit", sha)
			writeJSON(w, http.StatusOK, gen.Skill{Name: sk.Name, Description: sk.Description, HasScripts: sk.HasScripts, Files: sk.Files, UsedBy: []string{}})
			return
		}
	}
	s.internal(w, "find added skill", errors.New("missing after save"))
}
