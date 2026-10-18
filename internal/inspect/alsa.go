package inspect

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// MixerControl is the parsed output of `amixer -c CARD sget CONTROL`.
type MixerControl struct {
	Name     string `json:"name"`
	Found    bool   `json:"found"`
	EnumItem string `json:"enum_item,omitempty"`
	Percent  []int  `json:"percent,omitempty"`
	SwitchOn []bool `json:"switch_on,omitempty"`
}

// Audible means the pin can emit sound: at least one channel switched on with volume > 0.
func (m MixerControl) Audible() bool {
	for i, on := range m.SwitchOn {
		if on && i < len(m.Percent) && m.Percent[i] > 0 {
			return true
		}
	}
	return false
}

type AlsaState struct {
	Card     string       `json:"card"`
	AutoMute MixerControl `json:"auto_mute"`
	// Headphone drives the FRONT jack (AKG). "Front" drives the REAR line-out
	// (SP166) on this ALC887-VD despite its name — verified during setup.
	Headphone MixerControl `json:"headphone"`
	Front     MixerControl `json:"front"`
}

var (
	reEnumItem = regexp.MustCompile(`Item0:\s*'([^']*)'`)
	rePlayback = regexp.MustCompile(`Playback\s+\d+\s+\[(\d+)%\](?:\s+\[[^\]]*dB\])?\s+\[(on|off)\]`)
)

// ParseAmixerSget is a pure parser so it can be tested on real captured output.
func ParseAmixerSget(name, output string) MixerControl {
	m := MixerControl{Name: name}
	if !strings.Contains(output, "Simple mixer control") {
		return m
	}
	m.Found = true
	if match := reEnumItem.FindStringSubmatch(output); match != nil {
		m.EnumItem = match[1]
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !(strings.HasPrefix(line, "Front Left:") || strings.HasPrefix(line, "Front Right:") || strings.HasPrefix(line, "Mono:")) {
			continue
		}
		if match := rePlayback.FindStringSubmatch(line); match != nil {
			pct, _ := strconv.Atoi(match[1])
			m.Percent = append(m.Percent, pct)
			m.SwitchOn = append(m.SwitchOn, match[2] == "on")
		}
	}
	return m
}

// ProbeAlsa reads the three controls the destination switch depends on.
func ProbeAlsa(ctx context.Context, r CommandRunner, card string) (AlsaState, error) {
	st := AlsaState{Card: card}
	read := func(control string) (MixerControl, error) {
		out, err := r.Run(ctx, "amixer", "-c", card, "sget", control)
		if err != nil {
			return MixerControl{Name: control}, err
		}
		return ParseAmixerSget(control, string(out)), nil
	}
	var err error
	if st.AutoMute, err = read("Auto-Mute Mode"); err != nil {
		return st, err
	}
	if st.Headphone, err = read("Headphone"); err != nil {
		return st, err
	}
	if st.Front, err = read("Front"); err != nil {
		return st, err
	}
	return st, nil
}
