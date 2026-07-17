package main

import (
	"encoding/json"
	"os/exec"
	"sort"
)

type recipe struct {
	Name   string
	Doc    string
	Group  string
	Params []string
}

// loadRecipes reads public recipes from `just --dump`.
func loadRecipes(justfile string) ([]recipe, error) {
	out, err := exec.Command("just", "--justfile", justfile,
		"--dump", "--dump-format", "json").Output()
	if err != nil {
		return nil, err
	}
	var dump struct {
		Recipes map[string]struct {
			Doc        string `json:"doc"`
			Private    bool   `json:"private"`
			Attributes []any  `json:"attributes"`
			Parameters []struct {
				Name string `json:"name"`
			} `json:"parameters"`
		} `json:"recipes"`
	}
	if err := json.Unmarshal(out, &dump); err != nil {
		return nil, err
	}

	var rs []recipe
	for name, r := range dump.Recipes {
		if r.Private || name[0] == '_' {
			continue
		}
		group := "Other"
		for _, attr := range r.Attributes {
			if m, ok := attr.(map[string]any); ok {
				if g, ok := m["group"].(string); ok {
					group = g
				}
			}
		}
		rec := recipe{Name: name, Doc: r.Doc, Group: group}
		for _, p := range r.Parameters {
			rec.Params = append(rec.Params, p.Name)
		}
		rs = append(rs, rec)
	}
	sort.Slice(rs, func(i, j int) bool {
		ri, rj := groupRank(rs[i].Group), groupRank(rs[j].Group)
		if ri != rj {
			return ri < rj
		}
		if rs[i].Group != rs[j].Group {
			return rs[i].Group < rs[j].Group
		}
		return rs[i].Name < rs[j].Name
	})
	return rs, nil
}

// groupRank fixes the menu section order; groups not listed sort
// alphabetically between Hardening and the Power section at the bottom.
func groupRank(group string) int {
	switch group {
	case "System":
		return 0
	case "Configuration":
		return 1
	case "Network":
		return 2
	case "Apps":
		return 3
	case "Hardening":
		return 4
	case "Power":
		return 99
	default:
		return 50
	}
}
