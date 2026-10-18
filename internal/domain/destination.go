package domain

import (
	"errors"
	"fmt"
	"strings"
)

type DestinationID string

const (
	DestAKG     DestinationID = "akg"
	DestSP166   DestinationID = "sp166"
	DestUnknown DestinationID = "unknown"
)

var (
	ErrUnknownDestination = errors.New("unknown output destination")
	ErrIncompatiblePreset = errors.New("preset is incompatible with the target output destination")
	ErrEmptyPresetName    = errors.New("preset name cannot be empty")
)

// Destination holds the routing configuration, hardware ALSA controls,
// calibrated physical ceiling, and preset compatibility rules for one output port.
type Destination struct {
	ID                  DestinationID   `json:"id"`
	Label               string          `json:"label"`
	CardName            string          `json:"card_name"`
	PhysicalSinkName    string          `json:"physical_sink_name"`
	RouteName           string          `json:"route_name"`
	AlsaActiveControl   string          `json:"alsa_active_control"`
	AlsaInactiveControl string          `json:"alsa_inactive_control"`
	PhysicalCeiling     PercentageLevel `json:"physical_ceiling"`
	BasePreset          string          `json:"base_preset"`
	PresetPrefixes      []string        `json:"preset_prefixes"`
	allowedExplicit     map[string]struct{}
}

// DestinationConfig defines construction parameters for a Destination.
type DestinationConfig struct {
	ID                  DestinationID
	Label               string
	CardName            string
	PhysicalSinkName    string
	RouteName           string
	AlsaActiveControl   string
	AlsaInactiveControl string
	PhysicalCeilingPct  int
	BasePreset          string
	PresetPrefixes      []string
	ExplicitPresets     []string
}

// NewDestination constructs and validates an encapsulated Destination.
func NewDestination(cfg DestinationConfig) (*Destination, error) {
	if cfg.ID != DestAKG && cfg.ID != DestSP166 {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDestination, cfg.ID)
	}
	if strings.TrimSpace(cfg.BasePreset) == "" {
		return nil, ErrEmptyPresetName
	}
	if cfg.AlsaActiveControl == "" || cfg.AlsaInactiveControl == "" || cfg.AlsaActiveControl == cfg.AlsaInactiveControl {
		return nil, errors.New("ALSA active and inactive controls must be non-empty and distinct")
	}

	allowed := make(map[string]struct{}, len(cfg.ExplicitPresets)+1)
	allowed[cfg.BasePreset] = struct{}{}
	for _, p := range cfg.ExplicitPresets {
		if t := strings.TrimSpace(p); t != "" {
			allowed[t] = struct{}{}
		}
	}

	prefixes := make([]string, 0, len(cfg.PresetPrefixes))
	for _, pref := range cfg.PresetPrefixes {
		if t := strings.TrimSpace(pref); t != "" {
			prefixes = append(prefixes, t)
		}
	}

	return &Destination{
		ID:                  cfg.ID,
		Label:               cfg.Label,
		CardName:            cfg.CardName,
		PhysicalSinkName:    cfg.PhysicalSinkName,
		RouteName:           cfg.RouteName,
		AlsaActiveControl:   cfg.AlsaActiveControl,
		AlsaInactiveControl: cfg.AlsaInactiveControl,
		PhysicalCeiling:     NewPercentageLevel(cfg.PhysicalCeilingPct),
		BasePreset:          cfg.BasePreset,
		PresetPrefixes:      prefixes,
		allowedExplicit:     allowed,
	}, nil
}

// DefaultAKGDestination returns the calibrated front headphone destination configuration.
func DefaultAKGDestination() *Destination {
	dest, _ := NewDestination(DestinationConfig{
		ID:                  DestAKG,
		Label:               "Fone AKG K52",
		CardName:            "alsa_card.pci-0000_00_1f.3",
		PhysicalSinkName:    "alsa_output.pci-0000_00_1f.3.analog-stereo",
		RouteName:           "analog-output-headphones",
		AlsaActiveControl:   "Headphone",
		AlsaInactiveControl: "Front",
		PhysicalCeilingPct:  97,
		BasePreset:          "AKG K52 - Uso diario aprovado",
		PresetPrefixes:      []string{"AKG", "[AKG]"},
	})
	return dest
}

// DefaultSP166Destination returns the calibrated rear speaker destination configuration.
func DefaultSP166Destination() *Destination {
	dest, _ := NewDestination(DestinationConfig{
		ID:                  DestSP166,
		Label:               "Caixas SP166",
		CardName:            "alsa_card.pci-0000_00_1f.3",
		PhysicalSinkName:    "alsa_output.pci-0000_00_1f.3.analog-stereo",
		RouteName:           "analog-output-lineout",
		AlsaActiveControl:   "Front",
		AlsaInactiveControl: "Headphone",
		PhysicalCeilingPct:  100,
		BasePreset:          "SP166 - Uso diario aprovado",
		PresetPrefixes:      []string{"SP166", "Caixas", "[SP166]"},
	})
	return dest
}

// IsPresetCompatible enforces safety rule U09: never allow applying speaker presets
// to headphones or vice versa.
func (d *Destination) IsPresetCompatible(presetName string) bool {
	trimmed := strings.TrimSpace(presetName)
	if trimmed == "" {
		return false
	}
	if _, ok := d.allowedExplicit[trimmed]; ok {
		return true
	}
	upper := strings.ToUpper(trimmed)
	for _, prefix := range d.PresetPrefixes {
		if strings.HasPrefix(upper, strings.ToUpper(prefix)) {
			return true
		}
	}
	return false
}

// FilterCompatiblePresets returns safe presets for this destination,
// placing BasePreset first if available.
func (d *Destination) FilterCompatiblePresets(allPresets []string) []string {
	var result []string
	seen := make(map[string]struct{}, len(allPresets))

	for _, p := range allPresets {
		if p == d.BasePreset {
			result = append(result, p)
			seen[p] = struct{}{}
			break
		}
	}
	for _, p := range allPresets {
		if _, exists := seen[p]; exists {
			continue
		}
		if d.IsPresetCompatible(p) {
			result = append(result, p)
			seen[p] = struct{}{}
		}
	}
	return result
}
