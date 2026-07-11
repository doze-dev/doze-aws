package console

import (
	"encoding/json"
	"sort"
	"strings"
)

// SSM parameters are path-addressed (/app/db/host). The flat list buries that
// structure; ssmGroups buckets parameters by their parent path so the list pane
// reads like a tree — one dimmed folder header per path, leaves under it.

type ssmLeaf struct {
	Name string // full parameter name (the link + filter target)
	Leaf string // display label: the last path segment
	Type string
}

type ssmGroup struct {
	Dir   string // parent path ("/app/db"), or "" for un-pathed names
	Names string // JSON array of the group's full names, for the Alpine filter
	Rows  []ssmLeaf
}

// ssmGroups groups a parameter list by parent path, sorted, folders first by
// name. Called from the ssm_lp template so every caller keeps passing "List".
func ssmGroups(list []Parameter) []ssmGroup {
	byDir := map[string][]ssmLeaf{}
	for _, p := range list {
		dir, leaf := splitParamPath(p.Name)
		byDir[dir] = append(byDir[dir], ssmLeaf{Name: p.Name, Leaf: leaf, Type: p.Type})
	}
	groups := make([]ssmGroup, 0, len(byDir))
	for dir, rows := range byDir {
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		names := make([]string, len(rows))
		for i, r := range rows {
			names[i] = r.Name
		}
		raw, _ := json.Marshal(names)
		groups = append(groups, ssmGroup{Dir: dir, Names: string(raw), Rows: rows})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Dir < groups[j].Dir })
	return groups
}

// splitParamPath returns the parent path and the leaf segment. "/app/db/host"
// -> ("/app/db", "host"); "flat" -> ("", "flat").
func splitParamPath(name string) (dir, leaf string) {
	i := strings.LastIndex(name, "/")
	if i < 0 {
		return "", name
	}
	dir = name[:i]
	if dir == "" {
		dir = "/" // a top-level "/name" groups under root
	}
	return dir, name[i+1:]
}
