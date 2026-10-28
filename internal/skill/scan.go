package skill

import (
	"bytes"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

type Skill struct {
	Name        string
	Description string
	Path        string
}

func Scan(filesystem afero.Fs, root string) ([]Skill, error) {
	var skills []Skill
	err := afero.Walk(filesystem, root, func(path string, info fs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() && info.Name() == ".git" && path != root {
			return filepath.SkipDir
		}
		if info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || info.Name() != "SKILL.md" {
			return nil
		}
		content, err := afero.ReadFile(filesystem, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		metadata, err := parseMetadata(content, filepath.Base(filepath.Dir(path)))
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		metadata.Path, err = filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		metadata.Path = filepath.ToSlash(metadata.Path)
		skills = append(skills, metadata)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan skills: %w", err)
	}
	return skills, nil
}

func parseMetadata(content []byte, fallback string) (Skill, error) {
	metadata := Skill{Name: fallback, Description: fallback}
	lines := bytes.Split(bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n")), []byte("\n"))
	if len(lines) == 0 || string(lines[0]) != "---" {
		return metadata, nil
	}
	for index := 1; index < len(lines); index++ {
		if string(lines[index]) != "---" {
			continue
		}
		var fields struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal(bytes.Join(lines[1:index], []byte("\n")), &fields); err != nil {
			return Skill{}, err
		}
		if name := strings.TrimSpace(fields.Name); name != "" {
			metadata.Name = name
		}
		if description := strings.TrimSpace(fields.Description); description != "" {
			metadata.Description = description
		}
		return metadata, nil
	}
	return Skill{}, fmt.Errorf("unterminated frontmatter")
}
