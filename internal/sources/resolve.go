package sources

import (
	"sort"

	"filefin/internal/importer"
)

// Row statuses: how the merge settled one field.
const (
	StatusEmpty    = "empty"    // no source has a value
	StatusSingle   = "single"   // one source has a value
	StatusAgree    = "agree"    // the sources agree
	StatusDiffer   = "differ"   // they differ where that is expected (prose, lists)
	StatusConflict = "conflict" // they disagree; the value stays until someone decides
	StatusRule     = "rule"     // they disagree and a library rule decided
	StatusChosen   = "chosen"   // pinned to a source (or the union) for this item
	StatusManual   = "manual"   // pinned to the value it has
	StatusLocal    = "local"    // the value matches no source (an import, a hand edit): kept
)

// Row is one field of the merge: its value afterwards, what each source says, and how the
// merge decided.
type Row struct {
	Field    string              `json:"field"`
	Label    string              `json:"label"`
	Group    string              `json:"group"`
	Current  []string            `json:"current"`
	Values   map[string][]string `json:"values"`
	Status   string              `json:"status"`
	Class    string              `json:"class,omitempty"`
	Choice   string              `json:"choice,omitempty"`
	Rule     []string            `json:"rule,omitempty"`
	Pickable bool                `json:"pickable"`
	List     bool                `json:"list"`
	Union    bool                `json:"union"`
}

// Conflicts returns the rows still waiting for a decision.
func Conflicts(rows []Row) []Row {
	var out []Row
	for _, r := range rows {
		if r.Status == StatusConflict {
			out = append(out, r)
		}
	}
	return out
}

// Present lists the sources an item has a usable snapshot of, in preference order.
func Present(m importer.Meta) []string {
	var out []string
	for _, src := range known(m) {
		if m.Sources[src].Usable() {
			out = append(out, src)
		}
	}
	return out
}

// known is every source in the item, the default order first and any other one after it.
func known(m importer.Meta) []string {
	out := append([]string(nil), Order...)
	var extra []string
	for src := range m.Sources {
		if !contains(out, src) {
			extra = append(extra, src)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// Resolve merges an item's snapshots into its top-level fields and reports, field by field,
// how it decided. rules maps a field to the library-wide source order an admin chose for it.
// It is idempotent: resolving the result again changes nothing. Values nobody's source holds
// (a Plex import, an older hand edit) are never overwritten, and a pinned field keeps its pin.
func Resolve(m importer.Meta, rules map[string][]string) (importer.Meta, []Row) {
	m.Metadata = copyMap(m.Metadata)
	m.Ratings = copyMap(m.Ratings)
	present := Present(m)
	rows := make([]Row, 0, len(Fields))
	for _, f := range Fields {
		rows = append(rows, resolveField(&m, f, present, rules))
	}
	for _, src := range present {
		for k, v := range m.Sources[src].Ratings {
			if v != "" && m.Ratings[k] == "" {
				if m.Ratings == nil {
					m.Ratings = map[string]string{}
				}
				m.Ratings[k] = v
			}
		}
	}
	return m, rows
}

func resolveField(m *importer.Meta, f Field, present []string, rules map[string][]string) Row {
	row := Row{
		Field: f.Key, Label: f.Label, Group: f.Group, Pickable: f.Pickable,
		List: f.List, Union: f.Policy == PolicyUnion, Values: map[string][]string{},
	}
	var have []string // sources with a value, in preference order
	for _, src := range present {
		if v := snapshotValue(m.Sources[src], f); len(v) > 0 {
			row.Values[src] = v
			have = append(have, src)
		}
	}
	for i := 0; i < len(have); i++ {
		for j := i + 1; j < len(have); j++ {
			c := compare(f, row.Values[have[i]], row.Values[have[j]], m.Sources[have[i]], m.Sources[have[j]])
			if c == ClassIdentity || (c == ClassField && row.Class == "") {
				row.Class = c
			}
		}
	}
	var cur []string
	if !f.Virtual {
		cur = value(*m, f)
	}
	set := func(v []string) {
		if !f.Virtual {
			setValue(m, f, v)
		}
	}
	ruleFirst := ""
	if f.Pickable {
		for _, src := range rules[f.Key] {
			if len(row.Values[src]) > 0 {
				ruleFirst, row.Rule = src, rules[f.Key]
				break
			}
		}
	}
	orderFirst := ""
	if len(have) > 0 {
		orderFirst = have[0]
	}
	choice := ""
	if f.Pickable {
		choice = m.Choices[f.Key]
	}
	row.Choice = choice

	switch {
	case choice == ChoiceManual:
		row.Status = StatusManual
	case choice == ChoiceUnion && f.List && len(have) > 0:
		set(union(row.Values, have))
		row.Status = StatusChosen
	case choice != "" && len(row.Values[choice]) > 0:
		set(row.Values[choice])
		row.Status = StatusChosen
	case len(have) == 0:
		row.Status = StatusEmpty
	case len(cur) > 0 && !derived(cur, row.Values, have, f):
		row.Status = StatusLocal
	case f.Policy == PolicyUnion:
		if ruleFirst != "" {
			set(row.Values[ruleFirst])
		} else {
			set(union(row.Values, have))
		}
		row.Status = spread(row.Values, have, f)
	case f.Policy == PolicyProse || f.Policy == PolicyFill:
		switch {
		case ruleFirst != "":
			set(row.Values[ruleFirst])
		case len(cur) == 0:
			set(row.Values[orderFirst])
		}
		row.Status = spread(row.Values, have, f)
	case row.Class == "":
		if len(cur) == 0 {
			set(row.Values[orderFirst])
		}
		row.Status = StatusAgree
		if len(have) == 1 {
			row.Status = StatusSingle
		}
	case ruleFirst != "" && row.Class == ClassField:
		set(row.Values[ruleFirst])
		row.Status = StatusRule
	default:
		if len(cur) == 0 {
			set(row.Values[orderFirst])
		}
		row.Status = StatusConflict
	}
	row.Current = []string{} // never null on the wire: the merge view reads it as a list
	if !f.Virtual {
		row.Current = append(row.Current, value(*m, f)...)
	}
	return row
}

// derived reports whether a current value came from a source: it equals one source's value,
// or the union of them for a list merged that way.
func derived(cur []string, vals map[string][]string, have []string, f Field) bool {
	for _, src := range have {
		if sameValues(cur, vals[src], f.List) {
			return true
		}
	}
	return f.List && sameValues(cur, union(vals, have), true)
}

// spread is the status of a field whose sources may differ without conflicting.
func spread(vals map[string][]string, have []string, f Field) string {
	if len(have) == 1 {
		return StatusSingle
	}
	for _, src := range have[1:] {
		if !sameValues(vals[have[0]], vals[src], f.List) {
			return StatusDiffer
		}
	}
	return StatusAgree
}

// union combines the sources' entries in preference order, each entry once.
func union(vals map[string][]string, have []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, src := range have {
		for _, v := range vals[src] {
			if k := fold(v); !seen[k] {
				seen[k] = true
				out = append(out, v)
			}
		}
	}
	return out
}

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
