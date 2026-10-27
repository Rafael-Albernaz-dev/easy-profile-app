package inspect

import "strings"

// OutputPluginPath uses the already captured graph; no commands or polling.
// A path must reach the requested physical sink through connected native nodes.
func OutputPluginPath(g *Graph, sink, physical string) ([]string, bool) {
	var walk func(int, []string, map[int]bool) ([]string, bool)
	walk = func(id int, path []string, seen map[int]bool) ([]string, bool) {
		n := g.Nodes[id]
		if n == nil || seen[id] {
			return nil, false
		}
		if n.MediaClass == "Audio/Sink" && n.Name != sink {
			return path, (physical == "" || n.Name == physical) && len(path) > 0
		}
		if strings.HasPrefix(n.Name, "ee_soe_") && n.Name != "ee_soe_output_level" && n.Name != "ee_soe_spectrum" {
			path = append(append([]string{}, path...), strings.TrimPrefix(n.Name, "ee_soe_"))
		}
		seen[id] = true
		defer delete(seen, id)
		for _, l := range g.Links {
			if l.OutNode == id && (l.State == "active" || l.State == "paused") {
				if p, ok := walk(l.InNode, path, seen); ok {
					return p, true
				}
			}
		}
		return nil, false
	}
	for id, n := range g.Nodes {
		if n.Name == sink {
			if p, ok := walk(id, nil, map[int]bool{}); ok {
				return p, true
			}
		}
	}
	return nil, false
}

// Only an explicit idle/suspended sink without active input may defer DSP proof.
// Unknown state never grants the idle exception.
func OutputIsIdle(g *Graph, sink string) bool {
	var node *NodeInfo
	for _, n := range g.Nodes {
		if n.Name == sink {
			if node != nil {
				return false
			}
			node = n
		}
	}
	if node == nil || (node.State != "idle" && node.State != "suspended") {
		return false
	}
	for _, l := range g.Links {
		if l.InNode == node.ID && l.State == "active" {
			return false
		}
	}
	return true
}
