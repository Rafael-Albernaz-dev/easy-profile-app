package inspect

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// VolumeInfo holds the volume-related fields of a node's "Props" param.
// PipeWire stores channelVolumes as LINEAR gain. wpctl and GNOME display the
// CUBE ROOT of it, so linear 0.8479 is shown as 0.95 (95%). Never compare
// the two scales directly.
type VolumeInfo struct {
	ChannelVolumes []float64 `json:"channel_volumes,omitempty"`
	SoftVolumes    []float64 `json:"soft_volumes,omitempty"`
	MonitorVolumes []float64 `json:"monitor_volumes,omitempty"`
	Mute           *bool     `json:"mute,omitempty"`
	MonitorMute    *bool     `json:"monitor_mute,omitempty"`
}

func average(values []float64) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values)), true
}

// LinearAverage is the mean linear gain across channels.
func (v VolumeInfo) LinearAverage() (float64, bool) { return average(v.ChannelVolumes) }

// CubicPercent converts the linear gain to the percentage shown by wpctl/GNOME.
func (v VolumeInfo) CubicPercent() (int, bool) {
	avg, ok := v.LinearAverage()
	if !ok {
		return 0, false
	}
	return int(math.Round(math.Cbrt(avg) * 100)), true
}

// SoftDiffersFromChannel reports whether the software gain actually applied
// by the node differs from the requested channel volume.
func (v VolumeInfo) SoftDiffersFromChannel() bool {
	ch, ok1 := average(v.ChannelVolumes)
	soft, ok2 := average(v.SoftVolumes)
	return ok1 && ok2 && math.Abs(ch-soft) > 0.001
}

type NodeInfo struct {
	ID                    int        `json:"id"`
	Serial                int        `json:"object_serial"`
	Name                  string     `json:"name"`
	MediaClass            string     `json:"media_class"`
	DeviceID              int        `json:"device_id,omitempty"`
	ProfileName           string     `json:"device_profile,omitempty"`
	AppName               string     `json:"application,omitempty"`
	MonitorChannelVolumes *bool      `json:"monitor_channel_volumes,omitempty"`
	Volume                VolumeInfo `json:"volume"`
}

type RouteInfo struct {
	Index     int        `json:"index"`
	Device    int        `json:"device"`
	Name      string     `json:"name"`
	Direction string     `json:"direction"`
	Available string     `json:"available,omitempty"`
	Volume    VolumeInfo `json:"volume"`
}

type DeviceInfo struct {
	ID           int         `json:"id"`
	Name         string      `json:"name"`
	Serial       string      `json:"serial,omitempty"`
	VendorID     string      `json:"vendor_id,omitempty"`
	ProductID    string      `json:"product_id,omitempty"`
	ActiveRoutes []RouteInfo `json:"active_routes"`
	EnumRoutes   []RouteInfo `json:"enum_routes"`
}

type LinkInfo struct {
	ID      int    `json:"id"`
	OutNode int    `json:"output_node"`
	InNode  int    `json:"input_node"`
	State   string `json:"state"`
}

// Graph is a parsed, immutable view of one `pw-dump` snapshot.
// IDs are ephemeral: valid only for this snapshot.
type Graph struct {
	Nodes   map[int]*NodeInfo
	Devices map[int]*DeviceInfo
	Links   []LinkInfo
	// Defaults holds subject-0 keys of the "default" metadata (e.g. default.audio.sink).
	Defaults map[string]string
	// RestoreTargets holds "route-settings" restore entries that pin a stream class to a node name.
	RestoreTargets map[string]string
}

// --- raw pw-dump shapes (only the fields we need) ---

type rawObject struct {
	ID       int            `json:"id"`
	Type     string         `json:"type"`
	Info     *rawInfo       `json:"info"`
	Props    map[string]any `json:"props"`
	Metadata []rawMetaEntry `json:"metadata"`
}

type rawInfo struct {
	Props        map[string]any `json:"props"`
	Params       rawParams      `json:"params"`
	OutputNodeID int            `json:"output-node-id"`
	InputNodeID  int            `json:"input-node-id"`
	State        string         `json:"state"`
}

type rawParams struct {
	Props     []rawVolumeParam `json:"Props"`
	Route     []rawRoute       `json:"Route"`
	EnumRoute []rawRoute       `json:"EnumRoute"`
}

type rawVolumeParam struct {
	ChannelVolumes []float64 `json:"channelVolumes"`
	SoftVolumes    []float64 `json:"softVolumes"`
	MonitorVolumes []float64 `json:"monitorVolumes"`
	Mute           *bool     `json:"mute"`
	MonitorMute    *bool     `json:"monitorMute"`
}

type rawRoute struct {
	Index     int             `json:"index"`
	Device    int             `json:"device"`
	Name      string          `json:"name"`
	Direction string          `json:"direction"`
	Available string          `json:"available"`
	Props     *rawVolumeParam `json:"props"`
}

type rawMetaEntry struct {
	Subject int             `json:"subject"`
	Key     string          `json:"key"`
	Value   json.RawMessage `json:"value"`
}

// ParseGraph decodes a complete `pw-dump --no-colors` JSON array.
func ParseGraph(data []byte) (*Graph, error) {
	var objects []rawObject
	if err := json.Unmarshal(data, &objects); err != nil {
		return nil, fmt.Errorf("pw-dump JSON: %w", err)
	}
	g := &Graph{
		Nodes:          map[int]*NodeInfo{},
		Devices:        map[int]*DeviceInfo{},
		Defaults:       map[string]string{},
		RestoreTargets: map[string]string{},
	}
	for _, obj := range objects {
		switch {
		case strings.HasSuffix(obj.Type, ":Node") && obj.Info != nil:
			g.Nodes[obj.ID] = parseNode(obj.ID, obj.Info)
		case strings.HasSuffix(obj.Type, ":Device") && obj.Info != nil:
			g.Devices[obj.ID] = parseDevice(obj.ID, obj.Info)
		case strings.HasSuffix(obj.Type, ":Link") && obj.Info != nil:
			g.Links = append(g.Links, LinkInfo{
				ID: obj.ID, OutNode: obj.Info.OutputNodeID, InNode: obj.Info.InputNodeID, State: obj.Info.State,
			})
		case strings.HasSuffix(obj.Type, ":Metadata"):
			g.absorbMetadata(propString(obj.Props, "metadata.name"), obj.Metadata)
		}
	}
	return g, nil
}

func parseNode(id int, info *rawInfo) *NodeInfo {
	p := info.Props
	n := &NodeInfo{
		ID:          id,
		Serial:      propInt(p, "object.serial"),
		Name:        propString(p, "node.name"),
		MediaClass:  propString(p, "media.class"),
		DeviceID:    propInt(p, "device.id"),
		ProfileName: propString(p, "device.profile.name"),
		AppName:     propString(p, "application.name"),
		Volume:      mergeVolumeParams(info.Params.Props),
	}
	if v, ok := p["monitor.channel-volumes"].(bool); ok {
		n.MonitorChannelVolumes = &v
	}
	return n
}

// mergeVolumeParams takes the first non-empty value of each field, because the
// Props param can be split across several objects.
func mergeVolumeParams(params []rawVolumeParam) VolumeInfo {
	var v VolumeInfo
	for _, p := range params {
		if v.ChannelVolumes == nil && len(p.ChannelVolumes) > 0 {
			v.ChannelVolumes = p.ChannelVolumes
		}
		if v.SoftVolumes == nil && len(p.SoftVolumes) > 0 {
			v.SoftVolumes = p.SoftVolumes
		}
		if v.MonitorVolumes == nil && len(p.MonitorVolumes) > 0 {
			v.MonitorVolumes = p.MonitorVolumes
		}
		if v.Mute == nil && p.Mute != nil {
			v.Mute = p.Mute
		}
		if v.MonitorMute == nil && p.MonitorMute != nil {
			v.MonitorMute = p.MonitorMute
		}
	}
	return v
}

func parseDevice(id int, info *rawInfo) *DeviceInfo {
	p := info.Props
	d := &DeviceInfo{
		ID:        id,
		Name:      propString(p, "device.name"),
		Serial:    propString(p, "device.serial"),
		VendorID:  propString(p, "device.vendor.id"),
		ProductID: propString(p, "device.product.id"),
	}
	convert := func(r rawRoute) RouteInfo {
		ri := RouteInfo{Index: r.Index, Device: r.Device, Name: r.Name, Direction: r.Direction, Available: r.Available}
		if r.Props != nil {
			ri.Volume = mergeVolumeParams([]rawVolumeParam{*r.Props})
		}
		return ri
	}
	for _, r := range info.Params.Route {
		d.ActiveRoutes = append(d.ActiveRoutes, convert(r))
	}
	for _, r := range info.Params.EnumRoute {
		d.EnumRoutes = append(d.EnumRoutes, convert(r))
	}
	return d
}

func (g *Graph) absorbMetadata(name string, entries []rawMetaEntry) {
	for _, e := range entries {
		switch name {
		case "default":
			if e.Subject == 0 {
				if v := metadataName(e.Value); v != "" {
					g.Defaults[e.Key] = v
				}
			}
		case "route-settings":
			if strings.HasPrefix(e.Key, "restore.stream.") {
				if target := metadataField(e.Value, "target-node"); target != "" {
					g.RestoreTargets[e.Key] = target
				}
			}
		}
	}
}

// metadataName extracts {"name": "..."} whether the value arrives as a JSON
// object (pw-dump 1.0) or as a JSON-encoded string (older dumps / pw-metadata).
func metadataName(raw json.RawMessage) string { return metadataField(raw, "name") }

func metadataField(raw json.RawMessage, field string) string {
	obj := decodeMetadataObject(raw)
	if obj == nil {
		return ""
	}
	s, _ := obj[field].(string)
	return s
}

func decodeMetadataObject(raw json.RawMessage) map[string]any {
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		return obj
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && json.Unmarshal([]byte(s), &obj) == nil {
		return obj
	}
	return nil
}

func propString(p map[string]any, key string) string {
	switch v := p[key].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%g", v)
	default:
		return ""
	}
}

func propInt(p map[string]any, key string) int {
	switch v := p[key].(type) {
	case float64:
		return int(v)
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return 0
}
