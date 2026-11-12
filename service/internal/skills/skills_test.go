package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const tripSkill = "---\nname: trip-planner\ndescription: Plan trips the way the owner likes them.\n---\nAlways prefer trains.\n"

func TestSaveListAndShowOnlyTheAgentsSkills(t *testing.T) {
	ctx := context.Background()
	l := Library{Dir: t.TempDir()}
	if err := l.Save(ctx, "trip-planner", tripSkill); err != nil {
		t.Fatal(err)
	}
	if err := l.Save(ctx, "taxes", "---\nname: taxes\ndescription: Do taxes.\n---\nKeep receipts.\n"); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(l.Dir, "taxes", "scripts"), 0o700)
	os.WriteFile(filepath.Join(l.Dir, "taxes", "scripts", "sum.py"), []byte("print(1)"), 0o600)

	got, err := l.List(ctx)
	if err != nil || len(got) != 2 || got[0].Name != "taxes" || !got[0].HasScripts || got[1].HasScripts {
		t.Fatalf("list = %+v, %v", got, err)
	}
	src := l.Source([]string{"trip-planner"})
	fms, err := src.ListFrontmatters(ctx)
	if err != nil || len(fms) != 1 || fms[0].Name != "trip-planner" {
		t.Fatalf("agent sees %+v, %v", fms, err)
	}
	if _, err := src.LoadInstructions(ctx, "taxes"); err == nil {
		t.Fatal("an agent read a skill it doesn't have")
	}
	if text, err := src.LoadInstructions(ctx, "trip-planner"); err != nil || text == "" {
		t.Fatalf("instructions = %q, %v", text, err)
	}

	for name, content := range map[string]string{
		"Bad Name":  tripSkill,
		"other":     tripSkill, // the frontmatter names a different skill
		"no-desc":   "---\nname: no-desc\n---\nx\n",
		"no-header": "just text",
	} {
		if l.Save(ctx, name, content) == nil {
			t.Errorf("%s was saved", name)
		}
	}
	if err := l.Delete("taxes"); err != nil {
		t.Fatal(err)
	}
	if l.Delete("../etc") == nil {
		t.Fatal("deleted outside the library")
	}
}
