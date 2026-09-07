package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDirParsesSkillFrontmatter(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "review")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: code-review\ndescription: Review code carefully\n---\nCheck tests and error handling."
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "code-review" || skills[0].Content != "Check tests and error handling." {
		t.Fatalf("skills = %+v", skills)
	}
	if !strings.Contains(BuildContext(skills, 1000), "Skill: code-review") {
		t.Fatal("skill context did not include skill name")
	}
}
