package skills

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Shipped skills go in once; a skill the owner removed stays removed.
func TestBuiltinSkillsInstallOnce(t *testing.T) {
	dir := t.TempDir()
	lib := Library{Dir: filepath.Join(dir, "skills")}
	markers := filepath.Join(dir, "builtin-skills")
	added, err := lib.InstallBuiltins(context.Background(), markers)
	if err != nil || len(added) != 1 || added[0].Name != "free-data-apis" || added[0].Agent != "ag_it" {
		t.Fatalf("added %v, %v", added, err)
	}
	list, _ := lib.List(context.Background())
	if len(list) != 1 || list[0].Name != "free-data-apis" || !strings.Contains(list[0].Description, "no key") {
		t.Fatalf("library = %+v", list)
	}
	if err := lib.Delete("free-data-apis"); err != nil {
		t.Fatal(err)
	}
	if added, err := lib.InstallBuiltins(context.Background(), markers); err != nil || len(added) != 0 {
		t.Fatalf("a removed skill came back: %v %v", added, err)
	}
}
