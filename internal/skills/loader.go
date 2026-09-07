package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Skill struct {
	Name        string
	Description string
	Content     string
	Path        string
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func LoadDir(root string) ([]Skill, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skills directory: %w", err)
	}
	var loaded []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "SKILL.md")
		skill, err := LoadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, skill)
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Name < loaded[j].Name })
	return loaded, nil
}

func LoadFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("read skill %s: %w", path, err)
	}
	front, body, err := splitFrontmatter(string(data))
	if err != nil {
		return Skill{}, fmt.Errorf("parse skill %s: %w", path, err)
	}
	var metadata frontmatter
	if front != "" {
		if err := yaml.Unmarshal([]byte(front), &metadata); err != nil {
			return Skill{}, fmt.Errorf("parse skill metadata: %w", err)
		}
	}
	name := strings.TrimSpace(metadata.Name)
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	return Skill{Name: name, Description: strings.TrimSpace(metadata.Description), Content: strings.TrimSpace(body), Path: path}, nil
}

func splitFrontmatter(content string) (string, string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", content, nil
	}
	rest := strings.TrimPrefix(content, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", fmt.Errorf("frontmatter is not closed")
	}
	body := strings.TrimPrefix(rest[end+len("\n---"):], "\n")
	return rest[:end], body, nil
}

func BuildContext(skills []Skill, budget int) string {
	if budget <= 0 {
		budget = 12000
	}
	var builder strings.Builder
	for _, skill := range skills {
		section := fmt.Sprintf("Skill: %s\nDescription: %s\n%s\n\n", skill.Name, skill.Description, skill.Content)
		if len([]rune(builder.String()))+len([]rune(section)) > budget {
			break
		}
		builder.WriteString(section)
	}
	return strings.TrimSpace(builder.String())
}
