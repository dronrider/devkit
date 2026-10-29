// Package drafttypes loads draft definitions from the kit and project layers.
package drafttypes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/subtoml"
)

const DirName = "draft-types"

// Definition describes the grooming rule and required result fields.
type Definition struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Version     int      `json:"version"`
	Skill       string   `json:"skill,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	DefaultType string   `json:"default_type"`
	Required    []string `json:"required"`
	File        string   `json:"file"`
	Project     bool     `json:"project"`
}

type Set struct {
	Types    map[string]Definition
	Names    []string
	Warnings []string
}

var keys = map[string]string{"name": subtoml.KindStr, "title": subtoml.KindStr, "version": subtoml.KindInt, "skill": subtoml.KindStr, "prompt": subtoml.KindStr, "default_type": subtoml.KindStr, "required": subtoml.KindArr}
var fields = map[string]bool{"title": true, "body": true, "type": true, "rank": true, "size": true, "accept": true, "assignee": true, "due": true, "control": true}

// Parse validates one complete definition. Skill existence is checked by Load.
func Parse(path string, data []byte) (Definition, error) {
	var d Definition
	doc, err := subtoml.Parse(path, string(data))
	if err != nil {
		return d, err
	}
	for _, section := range doc.Order {
		if section != "" {
			return d, fmt.Errorf("%s: неизвестная секция %s", path, section)
		}
	}
	tab := doc.Table("")
	for _, key := range tab.Keys {
		v, _ := tab.Get(key)
		kind, ok := keys[key]
		if !ok {
			return d, fmt.Errorf("%s: неизвестное поле %s", path, key)
		}
		if v.Kind != kind {
			return d, fmt.Errorf("%s: поле %s: жду %s", path, key, subtoml.KindNames[kind])
		}
	}
	d = Definition{Name: tab.Str("name"), Title: tab.Str("title"), Skill: tab.Str("skill"), Prompt: tab.Str("prompt"), DefaultType: tab.Str("default_type"), Required: tab.Arr("required"), File: path}
	if v, ok := tab.Get("version"); ok {
		d.Version = v.Int
	}
	base := strings.TrimSuffix(filepath.Base(path), ".toml")
	if !subtoml.BareKey(base) || d.Name != base {
		return d, fmt.Errorf("%s: name должен совпадать с именем файла", path)
	}
	if strings.TrimSpace(d.Title) == "" {
		return d, fmt.Errorf("%s: пустое поле title", path)
	}
	if d.Version != 1 {
		return d, fmt.Errorf("%s: version должен быть 1", path)
	}
	if (d.Skill == "") == (d.Prompt == "") {
		return d, fmt.Errorf("%s: укажите ровно одно из полей skill и prompt", path)
	}
	if d.Skill != "" && (!subtoml.BareKey(d.Skill) || strings.Contains(d.Skill, ".")) {
		return d, fmt.Errorf("%s: неверное поле skill", path)
	}
	switch d.DefaultType {
	case "bug", "task", "LLD":
	default:
		return d, fmt.Errorf("%s: неверное поле default_type", path)
	}
	seen := map[string]bool{}
	for _, f := range d.Required {
		if !fields[f] || seen[f] {
			return d, fmt.Errorf("%s: неверное поле required: %q", path, f)
		}
		seen[f] = true
	}
	return d, nil
}

// Load replaces whole files from the kit with project definitions of the same name.
func Load(kitDir, projectDir, skillsDir string) (*Set, error) {
	set := &Set{Types: map[string]Definition{}}
	for _, layer := range []struct {
		dir     string
		project bool
	}{{kitDir, false}, {projectDir, true}} {
		if layer.dir == "" {
			continue
		}
		entries, err := os.ReadDir(layer.dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			path := filepath.Join(layer.dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if layer.project && len(data) == 0 {
				set.Warnings = append(set.Warnings, fmt.Sprintf("%s: пуст, использован встроенный тип при наличии", path))
				continue
			}
			d, err := Parse(path, data)
			if err != nil {
				return nil, err
			}
			d.Project = layer.project
			set.Types[d.Name] = d
		}
	}
	for _, d := range set.Types {
		if d.Skill != "" {
			path := filepath.Join(skillsDir, d.Skill, "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				return nil, fmt.Errorf("%s: skill %q: %w", d.File, d.Skill, err)
			}
		}
		set.Names = append(set.Names, d.Name)
	}
	sort.Strings(set.Names)
	return set, nil
}

func KitDir(root, home string) string {
	var dirs []string
	if v := os.Getenv("DEVKIT_HOME"); v != "" {
		dirs = append(dirs, filepath.Join(v, "kit", DirName))
	}
	if root != "" {
		dirs = append(dirs, filepath.Join(root, ".devkit", "devkit", "kit", DirName), filepath.Join(root, "kit", DirName), filepath.Join(filepath.Dir(root), "devkit", "kit", DirName))
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, "projects", "devkit", "kit", DirName))
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

func ProjectDir(root string) string { return filepath.Join(root, ".devkit", DirName) }
