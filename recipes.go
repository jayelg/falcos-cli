package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type recipe struct {
	Name     string
	Doc      string
	Group    string
	Params   []string
	Confirm  string              // confirmation prompt; empty = no confirmation needed
	Progress bool                // recipe emits OSC 9;4 progress sequences
	Silent   bool                // suppress CLI overlay during execution
	Select   map[string][]string // parameter name -> selectable options, empty = freeform text
}

// falcosAttrs matches falcos-specific just attributes that just 1.55+ rejects
// as unknown. These are stripped before passing to just --dump/--list.
var falcosAttrs = regexp.MustCompile(`^\s*\[(silent|progress|select\(.*\))\]`)

// rawAttrs parses falcos-specific attributes from the raw justfile text,
// mapping recipe name -> parsed attributes. Only handles attributes that
// just 1.55+ rejects (silent, progress, select). confirm and group are
// handled by just natively.
func rawAttrs(content string) map[string]map[string]string {
	// Track the most recent attribute and the next recipe name.
	type pendingAttr struct {
		key string
		val string
	}
	var pending []pendingAttr
	result := make(map[string]map[string]string)

	recipeRe := regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9_-]*)\s*:`)
	attrRe := regexp.MustCompile(`^\s*\[(silent|progress|select\(([^)]*)\))\]`)

	for _, line := range strings.Split(content, "\n") {
		if m := attrRe.FindStringSubmatch(line); m != nil {
			switch m[1] {
			case "silent":
				pending = append(pending, pendingAttr{key: "silent", val: "true"})
			case "progress":
				pending = append(pending, pendingAttr{key: "progress", val: "true"})
			default:
				if strings.HasPrefix(m[1], "select(") {
					pending = append(pending, pendingAttr{key: "select", val: m[2]})
				}
			}
			continue
		}
		if m := recipeRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			if _, ok := result[name]; !ok {
				result[name] = make(map[string]string)
			}
			for _, p := range pending {
				result[name][p.key] = p.val
			}
			pending = nil
		}
	}
	return result
}

// stripFalcosAttrs removes falcos-specific attribute lines from the justfile
// content so that just 1.55+ (which rejects unknown attributes) can parse it.
func stripFalcosAttrs(content string) string {
	var b strings.Builder
	for _, line := range strings.Split(content, "\n") {
		if falcosAttrs.MatchString(line) {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(line)
	}
	return b.String()
}

// strippedJustfilePath reads the justfile, strips falcos-specific attributes,
// writes the result to a temp file, and returns the temp file path. The caller
// must remove the returned path when done. On error, returns the original path.
func strippedJustfilePath(orig string) string {
	raw, err := os.ReadFile(orig)
	if err != nil {
		return orig
	}
	stripped := stripFalcosAttrs(string(raw))
	tmpFile, err := os.CreateTemp("", "falcos-justfile-*.just")
	if err != nil {
		return orig
	}
	if _, err := tmpFile.WriteString(stripped); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return orig
	}
	tmpFile.Close()
	return tmpFile.Name()
}

// loadRecipes reads public recipes from the justfile, ordered by their
// position in the file (from `just --list`), with details from
// `just --dump --dump-format json`.
func loadRecipes(justfile string) ([]recipe, error) {
	raw, err := os.ReadFile(justfile)
	if err != nil {
		return nil, err
	}
	content := string(raw)

	// Extract falcos-specific attributes from the raw text before stripping.
	customAttrs := rawAttrs(content)

	// Use a stripped copy so just 1.55+ (which rejects unknown attributes)
	// can parse the justfile.
	jf := strippedJustfilePath(justfile)
	defer os.Remove(jf)

	// Recipe details (doc, group, params) from JSON dump on stripped file.
	out, err := exec.Command("just", "--justfile", jf,
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

	// Recipe order from `just --list --unsorted`.
	listOut, err := exec.Command("just", "--justfile", jf, "--list", "--unsorted").Output()
	if err != nil {
		return nil, err
	}

	// Parse --list output to extract group headers and recipe names.
	groupRe := regexp.MustCompile(`^    \[(.+)\]`)
	recipeRe := regexp.MustCompile(`^    ([a-zA-Z][a-zA-Z0-9_-]*)`)

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
				sections = append(sections, section{Group: "Other"})
				current = &sections[len(sections)-1]
			}
			current.Recipes = append(current.Recipes, m[1])
		}
	}

	// Group recipes by their [group(...)] attribute, preserving order.
	groups := make(map[string][]string)
	groupOrder := []string{}

	for _, sec := range sections {
		for _, name := range sec.Recipes {
			r, ok := dump.Recipes[name]
			if !ok {
				continue
			}
			g := sec.Group
			for _, attr := range r.Attributes {
				if m, ok := attr.(map[string]any); ok {
					if grp, ok := m["group"].(string); ok {
						g = grp
						break
					}
				}
			}
			if _, seen := groups[g]; !seen {
				groupOrder = append(groupOrder, g)
			}
			groups[g] = append(groups[g], name)
		}
	}

	// Build the flat recipe list, merging custom attributes from raw parsing.
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

			// Parse standard just attributes (confirm is native in just 1.55+).
			for _, attr := range r.Attributes {
				if m, ok := attr.(map[string]any); ok {
					if confirm, ok := m["confirm"].(string); ok {
						rec.Confirm = confirm
					}
				}
			}

			// Merge falcos-specific attributes parsed from raw text.
			if ca, ok := customAttrs[name]; ok {
				if ca["silent"] == "true" {
					rec.Silent = true
				}
				if ca["progress"] == "true" {
					rec.Progress = true
				}
				if sel, ok := ca["select"]; ok && sel != "" {
					param, opts, _ := strings.Cut(sel, ":")
					if param != "" && opts != "" {
						if rec.Select == nil {
							rec.Select = make(map[string][]string)
						}
						for _, o := range strings.Split(opts, "|") {
							if o != "" {
								rec.Select[param] = append(rec.Select[param], o)
							}
						}
					}
				}
			}

			rs = append(rs, rec)
		}
	}
	return rs, nil
}
