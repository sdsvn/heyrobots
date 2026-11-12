// Package skills keeps the owner's skills library: one folder per skill in
// the data folder, each with a SKILL.md in the format Claude Code uses. A
// task sees only the skills its agent lists, loaded with ADK's skill
// toolset. Skills are only instructions: they add no tools, so what a task
// can do still comes from its Mandate.
package skills

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"google.golang.org/adk/v2/tool/skilltoolset/skill"
	"gopkg.in/yaml.v3"
)

// MaxSkillBytes caps a SKILL.md saved from the app.
const MaxSkillBytes = 256 << 10

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Library is the skills folder.
type Library struct {
	Dir string
}

// Skill describes one skill in the library.
type Skill struct {
	Name        string
	Description string
	// HasScripts is true when the skill ships scripts. They are shown to a
	// task as text and never run outside the container sandbox.
	HasScripts bool
	Files      int
	ModTime    time.Time
}

// List reads the library. Folders without a valid SKILL.md are left out.
func (l Library) List(ctx context.Context) ([]Skill, error) {
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return nil, err
	}
	src := skill.NewFileSystemSource(os.DirFS(l.Dir))
	fms, err := src.ListFrontmatters(ctx)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, fm := range fms {
		s := Skill{Name: fm.Name, Description: fm.Description}
		root := filepath.Join(l.Dir, fm.Name)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() == SourceFile {
				return nil
			}
			s.Files++
			if rel, _ := filepath.Rel(root, p); strings.HasPrefix(rel, "scripts"+string(filepath.Separator)) {
				s.HasScripts = true
			}
			if info, err := d.Info(); err == nil && info.ModTime().After(s.ModTime) {
				s.ModTime = info.ModTime()
			}
			return nil
		})
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Save writes a one-file skill. Its frontmatter must name it.
func (l Library) Save(ctx context.Context, name, content string) error {
	if !validName.MatchString(name) {
		return errors.New("a skill name uses lower-case letters, digits and dashes")
	}
	if len(content) > MaxSkillBytes {
		return errors.New("SKILL.md is larger than 256 KB")
	}
	// Check it in a scratch folder first, so a bad file never replaces a
	// good one.
	tmp, err := os.MkdirTemp("", "skill")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, name), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, name, "SKILL.md"), []byte(content), 0o600); err != nil {
		return err
	}
	fm, err := skill.NewFileSystemSource(os.DirFS(tmp)).LoadFrontmatter(ctx, name)
	if err != nil {
		return fmt.Errorf("SKILL.md needs frontmatter with name: %s and a description: %w", name, err)
	}
	if strings.TrimSpace(fm.Description) == "" {
		return errors.New("SKILL.md's frontmatter needs a description, so agents know when to use it")
	}
	dir := filepath.Join(l.Dir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600)
}

// SourceFile records, inside a skill's folder, where an added skill came
// from: owner/repo/path@commit.
const SourceFile = ".source"

// Frontmatter reads the name and description from a SKILL.md.
func Frontmatter(content []byte) (name, description string, err error) {
	rest, ok := bytes.CutPrefix(bytes.TrimLeft(content, "\ufeff \r\n"), []byte("---"))
	end := bytes.Index(rest, []byte("\n---"))
	if !ok || end < 0 {
		return "", "", errors.New("SKILL.md has no frontmatter")
	}
	var fm struct{ Name, Description string }
	if err := yaml.Unmarshal(rest[:end], &fm); err != nil {
		return "", "", fmt.Errorf("SKILL.md's frontmatter can't be read: %w", err)
	}
	return strings.TrimSpace(fm.Name), strings.TrimSpace(fm.Description), nil
}

// SaveDir writes a whole skill folder, replacing a skill of the same name.
// files maps slash-separated paths inside the folder to their contents;
// SKILL.md must be among them and name the skill. It returns the name.
func (l Library) SaveDir(ctx context.Context, files map[string][]byte, source string) (string, error) {
	name, _, err := Frontmatter(files["SKILL.md"])
	if err != nil {
		return "", err
	}
	if !validName.MatchString(name) {
		return "", errors.New("the skill's name must use lower-case letters, digits and dashes")
	}
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return "", err
	}
	// Build it next to the library, so a bad folder never replaces a good
	// one and the move is a rename.
	tmp, err := os.MkdirTemp(l.Dir, ".add-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	for p, b := range files {
		if !filepath.IsLocal(filepath.FromSlash(p)) {
			return "", fmt.Errorf("the skill has a file outside its folder: %s", p)
		}
		dst := filepath.Join(tmp, name, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return "", err
		}
	}
	if source != "" {
		if err := os.WriteFile(filepath.Join(tmp, name, SourceFile), []byte(source+"\n"), 0o600); err != nil {
			return "", err
		}
	}
	fm, err := skill.NewFileSystemSource(os.DirFS(tmp)).LoadFrontmatter(ctx, name)
	if err != nil {
		return "", fmt.Errorf("SKILL.md can't be read: %w", err)
	}
	if strings.TrimSpace(fm.Description) == "" {
		return "", errors.New("SKILL.md's frontmatter needs a description, so agents know when to use it")
	}
	dir := filepath.Join(l.Dir, name)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return name, os.Rename(filepath.Join(tmp, name), dir)
}

// Delete removes a skill's folder.
func (l Library) Delete(name string) error {
	if !validName.MatchString(name) {
		return os.ErrNotExist
	}
	dir := filepath.Join(l.Dir, name)
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return os.ErrNotExist
	}
	return os.RemoveAll(dir)
}

// Source is the part of the library an agent may see.
func (l Library) Source(names []string) skill.Source {
	return skill.NewFileSystemSource(only{fsys: os.DirFS(l.Dir), names: names})
}

// only shows the named skill folders and nothing else.
type only struct {
	fsys  fs.FS
	names []string
}

func (o only) allowed(name string) bool {
	if name == "." {
		return true
	}
	first, _, _ := strings.Cut(path.Clean(name), "/")
	return slices.Contains(o.names, first)
}

func (o only) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) || !o.allowed(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return o.fsys.Open(name)
}

func (o only) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) || !o.allowed(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries, err := fs.ReadDir(o.fsys, name)
	if err != nil || name != "." {
		return entries, err
	}
	var out []fs.DirEntry
	for _, e := range entries {
		if slices.Contains(o.names, e.Name()) {
			out = append(out, e)
		}
	}
	return out, nil
}
