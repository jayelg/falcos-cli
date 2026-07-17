 package main

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
)

type recipe struct {
	Name   string
	Doc    string
	Group  string
	Params []string
}

// loadRecipes reads public recipes from the justfile, ordered by their
// position in the file (from `just --list`), with details from
// `just --dump --dump-format json`.
func loadRecipes(justfile string) ([]recipe, error) {
	// Recipe details (doc, group, params) from JSON dump.
	out, err := exec.Command("just", "--justfile", justfile,
		"--dump", "--dump-format", "json").Output()
	if err != nil {
		return nil, err
	}
	var dump struct {
		Recipes map[string]struct {
			Doc        string `json:"doc"`
			Attributes []any  `json:"attributes"`
			Parameters []struct {
				Name string `json:"name"`
			} `json:"parameters"`
		} `json:"recipes"`
	}
	if err := json.Unmarshal(out, &dump); err != nil {
		return nil, err
	}

	// Recipe order from `just --list --unsorted`, which preserves
	// Justfile position within each group and excludes private recipes.
	listOut, err := exec.Command("just", "--justfile", justfile, "--list", "--unsorted").Output()
	if err != nil {
		return nil, err
	}

	// Parse --list output to extract group headers and recipe names in
	// Justfile order. Lines like "    [Group Name]" start a new section;
	// lines like "    recipe-name ..." add to the current section.
	groupRe := regexp.MustCompile(`^    \[(.+)\]`)
	recipeRe := regexp.MustCompile(`^    ([a-zA-Z][a-zA-Z0-9_-]*)`)

	// Track group first-appearance order and recipe-to-group mapping.
	type section struct {
		Group   string
		Recipes []string
	}
	var sections []section
	var current *section

	for _, line := range strings.Split(string(listOut), "\n") {
		if m := groupRe.FindStringSubmatch(line); m != nil {
			sections = append(sections, section{Group: m[1]})
			current = &sections[len(sections)-1]
			continue
		}
		if m := recipeRe.FindStringSubmatch(line); m != nil {
			if current == nil {
				// Recipes before any [Group] header are ungrouped.
				sections = append(sections, section{Group: "Other"})
				current = &sections[len(sections)-1]
			}
			current.Recipes = append(current.Recipes, m[1])
		}
	}

	// Group recipes by their [group(...)] attribute from the justfile,
	// preserving the first-appearance order of each group from --list.
	// Recipes without a [group(...)] attribute use their section group
	// from --list instead.
	groups := make(map[string][]string)   // group → recipe names
	groupOrder := []string{}              // ordered unique groups

	for _, sec := range sections {
		for _, name := range sec.Recipes {
			r, ok := dump.Recipes[name]
			if !ok {
				continue
			}
			// Resolve the effective group: [group(...)] attribute wins,
			// fall back to the section group from --list.
			g := sec.Group
			for _, attr := range r.Attributes {
				if m, ok := attr.(map[string]any); ok {
					if grp, ok := m["group"].(string); ok {
						g = grp
						break
					}
				}
			}
			// Record group first-appearance order.
			if _, seen := groups[g]; !seen {
				groupOrder = append(groupOrder, g)
			}
			groups[g] = append(groups[g], name)
		}
	}

	// Build the flat recipe list: groups in first-appearance order,
	// recipes within each group in --list order.
	seen := map[string]bool{}
	var rs []recipe
	for _, g := range groupOrder {
		for _, name := range groups[g] {
			if seen[name] {
				continue
			}
			seen[name] = true
			r := dump.Recipes[name]
			rec := recipe{Name: name, Doc: r.Doc, Group: g}
			for _, p := range r.Parameters {
				rec.Params = append(rec.Params, p.Name)
			}
			rs = append(rs, rec)
		}
	}
	return rs, nil
}
