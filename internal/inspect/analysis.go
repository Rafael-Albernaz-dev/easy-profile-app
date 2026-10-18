package inspect

import (
	"sort"
	"strings"
)

// Identity holds the STABLE identifiers of the user's setup. Numeric
// PipeWire IDs are never stored here; they are resolved per snapshot.
type Identity struct {
	CardName      string `json:"card_name"`
	PhysicalSink  string `json:"physical_sink"`
	VirtualSink   string `json:"virtual_sink"`
	VirtualSource string `json:"virtual_source"`
	RouteAKG      string `json:"route_akg"`
	RouteSP166    string `json:"route_sp166"`
	MicVendorID   string `json:"mic_vendor_id"`
	MicProductID  string `json:"mic_product_id"`
	MicSerial     string `json:"mic_serial"`
	MicProfile    string `json:"mic_profile"`
}

func DefaultIdentity() Identity {
	return Identity{
		CardName:      "alsa_card.pci-0000_00_1f.3",
		PhysicalSink:  "alsa_output.pci-0000_00_1f.3.analog-stereo",
		VirtualSink:   "easyeffects_sink",
		VirtualSource: "easyeffects_source",
		RouteAKG:      "analog-output-headphones",
		RouteSP166:    "analog-output-lineout",
		MicVendorID:   "0x0a67",
		MicProductID:  "0xd117",
		MicSerial:     "MU900_KA-1333_20190805V001",
		MicProfile:    "iec958-stereo",
	}
}

const (
	DestAKG     = "akg"
	DestSP166   = "sp166"
	DestUnknown = "unknown"
)

// Capture is an application node reading from a microphone node.
type Capture struct {
	LinkID   int    `json:"link_id"`
	NodeID   int    `json:"node_id"`
	NodeName string `json:"node_name"`
	App      string `json:"application,omitempty"`
}

// RouteOption is an output route the card offers for one destination.
type RouteOption struct {
	Destination string `json:"destination"`
	Name        string `json:"name"`
	Index       int    `json:"index"`
	Available   string `json:"available"`
	Found       bool   `json:"found"`
}

// GraphAnalysis is what the snapshot means for the user's setup.
type GraphAnalysis struct {
	CardFound         bool          `json:"card_found"`
	CardID            int           `json:"card_id,omitempty"`
	ActiveOutputRoute string        `json:"active_output_route"`
	ActiveDestination string        `json:"active_destination"`
	ActiveRouteVolume VolumeInfo    `json:"active_route_volume"`
	RouteOptions      []RouteOption `json:"route_options"`

	PhysicalSink  *NodeInfo `json:"physical_sink,omitempty"`
	VirtualSink   *NodeInfo `json:"virtual_sink,omitempty"`
	VirtualSource *NodeInfo `json:"virtual_source,omitempty"`

	MicCandidates []*NodeInfo `json:"mic_candidates"`
	MicResolved   *NodeInfo   `json:"mic_resolved,omitempty"`

	DefaultSink      string `json:"default_sink"`
	DefaultSource    string `json:"default_source"`
	ConfiguredSink   string `json:"configured_sink"`
	ConfiguredSource string `json:"configured_source"`

	RawMicCaptures       []Capture         `json:"raw_mic_captures"`
	ProcessedCaptures    []Capture         `json:"processed_captures"`
	RestoreToPhysical    map[string]string `json:"restore_targets_physical_sink,omitempty"`
	PlaybackIntoPhysical []Capture         `json:"playback_bypassing_easyeffects"`
}

// isEasyEffectsInternal identifies nodes owned by EasyEffects' own pipeline.
func isEasyEffectsInternal(n *NodeInfo) bool {
	return n != nil && (strings.HasPrefix(n.Name, "ee_") || strings.HasPrefix(n.Name, "easyeffects"))
}

// Analyze resolves the stable identity against one snapshot.
func Analyze(g *Graph, id Identity) GraphAnalysis {
	a := GraphAnalysis{
		ActiveDestination: DestUnknown,
		DefaultSink:       g.Defaults["default.audio.sink"],
		DefaultSource:     g.Defaults["default.audio.source"],
		ConfiguredSink:    g.Defaults["default.configured.audio.sink"],
		ConfiguredSource:  g.Defaults["default.configured.audio.source"],
	}

	var card *DeviceInfo
	for _, d := range sortedDevices(g) {
		if d.Name == id.CardName {
			card = d
			break
		}
	}
	if card != nil {
		a.CardFound = true
		a.CardID = card.ID
		for _, r := range card.ActiveRoutes {
			if r.Direction == "Output" {
				a.ActiveOutputRoute = r.Name
				a.ActiveRouteVolume = r.Volume
			}
		}
		switch a.ActiveOutputRoute {
		case id.RouteAKG:
			a.ActiveDestination = DestAKG
		case id.RouteSP166:
			a.ActiveDestination = DestSP166
		}
		for _, want := range []struct{ dest, name string }{{DestAKG, id.RouteAKG}, {DestSP166, id.RouteSP166}} {
			opt := RouteOption{Destination: want.dest, Name: want.name}
			for _, r := range card.EnumRoutes {
				if r.Name == want.name && r.Direction == "Output" {
					opt.Index, opt.Available, opt.Found = r.Index, r.Available, true
				}
			}
			a.RouteOptions = append(a.RouteOptions, opt)
		}
	}

	micDevices := map[int]bool{}
	for _, d := range g.Devices {
		if strings.EqualFold(d.VendorID, id.MicVendorID) &&
			strings.EqualFold(d.ProductID, id.MicProductID) &&
			d.Serial == id.MicSerial {
			micDevices[d.ID] = true
		}
	}

	for _, n := range sortedNodes(g) {
		switch {
		case n.Name == id.PhysicalSink && n.MediaClass == "Audio/Sink":
			a.PhysicalSink = n
		case n.Name == id.VirtualSink && n.MediaClass == "Audio/Sink":
			a.VirtualSink = n
		case n.Name == id.VirtualSource && strings.HasPrefix(n.MediaClass, "Audio/Source"):
			a.VirtualSource = n
		case n.MediaClass == "Audio/Source" && micDevices[n.DeviceID] && n.ProfileName == id.MicProfile:
			a.MicCandidates = append(a.MicCandidates, n)
		}
	}
	// Same rule as the installed guard: exactly one candidate, otherwise refuse.
	if len(a.MicCandidates) == 1 {
		a.MicResolved = a.MicCandidates[0]
	}

	for _, l := range g.Links {
		consumer := g.Nodes[l.InNode]
		producer := g.Nodes[l.OutNode]
		if consumer == nil || producer == nil {
			continue
		}
		c := Capture{LinkID: l.ID, NodeID: consumer.ID, NodeName: consumer.Name, App: consumer.AppName}
		if a.MicResolved != nil && l.OutNode == a.MicResolved.ID && !isEasyEffectsInternal(consumer) {
			a.RawMicCaptures = append(a.RawMicCaptures, c)
		}
		if a.VirtualSource != nil && l.OutNode == a.VirtualSource.ID && !isEasyEffectsInternal(consumer) {
			a.ProcessedCaptures = append(a.ProcessedCaptures, c)
		}
		// An application writing straight into the physical sink skips the EasyEffects chain.
		if a.PhysicalSink != nil && l.InNode == a.PhysicalSink.ID && !isEasyEffectsInternal(producer) {
			a.PlaybackIntoPhysical = append(a.PlaybackIntoPhysical,
				Capture{LinkID: l.ID, NodeID: producer.ID, NodeName: producer.Name, App: producer.AppName})
		}
	}
	a.RawMicCaptures = dedupeByNode(a.RawMicCaptures)
	a.ProcessedCaptures = dedupeByNode(a.ProcessedCaptures)
	a.PlaybackIntoPhysical = dedupeByNode(a.PlaybackIntoPhysical)

	for key, target := range g.RestoreTargets {
		if target == id.PhysicalSink {
			if a.RestoreToPhysical == nil {
				a.RestoreToPhysical = map[string]string{}
			}
			a.RestoreToPhysical[key] = target
		}
	}
	return a
}

// dedupeByNode collapses the one-link-per-channel duplicates (FL/FR).
func dedupeByNode(in []Capture) []Capture {
	seen := map[int]bool{}
	out := make([]Capture, 0, len(in))
	for _, c := range in {
		if !seen[c.NodeID] {
			seen[c.NodeID] = true
			out = append(out, c)
		}
	}
	return out
}

func sortedNodes(g *Graph) []*NodeInfo {
	out := make([]*NodeInfo, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedDevices(g *Graph) []*DeviceInfo {
	out := make([]*DeviceInfo, 0, len(g.Devices))
	for _, d := range g.Devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
