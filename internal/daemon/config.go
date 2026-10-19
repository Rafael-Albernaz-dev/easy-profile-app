package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/toru/easy-profile-app/internal/domain"
	"io"
	"os"
	"strings"
	"time"
)

// Config contains the setup-specific names and calibration, separate from IPC/state logic.
type Config struct {
	AlsaCard                 string                     `json:"alsa_card"`
	VirtualSink              string                     `json:"virtual_sink"`
	VirtualSource            string                     `json:"virtual_source"`
	PhysicalMicrophonePrefix string                     `json:"physical_microphone_prefix"`
	StabilizationMS          int                        `json:"stabilization_ms"`
	Destinations             []domain.DestinationConfig `json:"destinations"`
}

func DefaultConfig() Config {
	destinations := []domain.DestinationConfig{}
	for _, dest := range []*domain.Destination{domain.DefaultAKGDestination(), domain.DefaultSP166Destination()} {
		destinations = append(destinations, domain.DestinationConfig{ID: dest.ID, Label: dest.Label, CardName: dest.CardName, PhysicalSinkName: dest.PhysicalSinkName, RouteName: dest.RouteName, AlsaActiveControl: dest.AlsaActiveControl, AlsaInactiveControl: dest.AlsaInactiveControl, PhysicalCeilingPct: dest.PhysicalCeiling.Percent(), BasePreset: dest.BasePreset, PresetPrefixes: dest.PresetPrefixes})
	}
	return Config{AlsaCard, VirtualSinkNode, VirtualSourceNode, PhysicalMicPrefix, 1200, destinations}
}
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("trailing data in config")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
func (c Config) Validate() error {
	for _, name := range []string{c.AlsaCard, c.VirtualSink, c.VirtualSource, c.PhysicalMicrophonePrefix} {
		if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\n\r") {
			return errors.New("invalid hardware identity")
		}
	}
	if c.VirtualSink == c.VirtualSource || c.StabilizationMS < 1000 || c.StabilizationMS > 5000 {
		return errors.New("invalid virtual nodes or stabilization window (1000..5000 ms)")
	}
	if len(c.Destinations) != 2 {
		return errors.New("exactly AKG and SP166 destinations required")
	}
	var akg, sp *domain.Destination
	for _, item := range c.Destinations {
		dest, err := domain.NewDestination(item)
		if err != nil {
			return err
		}
		if dest.PhysicalSinkName == "" || dest.CardName == "" || dest.RouteName == "" {
			return errors.New("missing destination hardware identity")
		}
		if dest.ID == domain.DestAKG {
			if akg != nil || item.PhysicalCeilingPct < 1 || item.PhysicalCeilingPct > 97 {
				return errors.New("AKG ceiling must be 1..97")
			}
			akg = dest
		} else {
			if sp != nil || item.PhysicalCeilingPct < 1 || item.PhysicalCeilingPct > 100 {
				return errors.New("SP166 ceiling must be 1..100")
			}
			sp = dest
		}
	}
	if akg == nil || sp == nil || akg.PhysicalSinkName != sp.PhysicalSinkName || akg.CardName != sp.CardName || akg.RouteName == sp.RouteName || akg.AlsaActiveControl != sp.AlsaInactiveControl || sp.AlsaActiveControl != akg.AlsaInactiveControl {
		return errors.New("destinations must share sink/card and isolate opposite pins/routes")
	}
	for _, d := range []*domain.Destination{akg, sp} {
		if (d.AlsaActiveControl != "Headphone" && d.AlsaActiveControl != "Front") || (d.AlsaInactiveControl != "Headphone" && d.AlsaInactiveControl != "Front") {
			return fmt.Errorf("unsupported ALSA pin %q", d.AlsaActiveControl)
		}
	}
	return nil
}
func (c Config) Controller() (*domain.AudioController, error) {
	return c.controller(nil, "")
}

// ControllerWithSession is the production construction path; tests explicitly inject a temporary store.
func (c Config) ControllerWithSession(store *domain.SessionStore, sessionID string) (*domain.AudioController, error) {
	if store == nil || sessionID == "" {
		return nil, errors.New("session store and graphical identity required")
	}
	return c.controller(store, sessionID)
}

func (c Config) controller(store *domain.SessionStore, sessionID string) (*domain.AudioController, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var akg, sp *domain.Destination
	for _, item := range c.Destinations {
		dest, err := domain.NewDestination(item)
		if err != nil {
			return nil, err
		}
		if dest.ID == domain.DestAKG {
			akg = dest
		} else {
			sp = dest
		}
	}
	if store != nil {
		return domain.NewSessionController(store, sessionID, akg, sp)
	}
	return domain.NewAudioControllerWithDestinations(akg, sp), nil
}
func (d *Daemon) Configure(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hardware = cfg
	d.stabilization = time.Duration(cfg.StabilizationMS) * time.Millisecond
	return nil
}
