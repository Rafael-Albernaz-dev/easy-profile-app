package alsa

import (
	"context"
	"fmt"
	"sync"

	"github.com/toru/easy-profile-app/internal/adapters"
)

// FakeAlsa is an in-memory test double implementing adapters.AlsaPort.
type FakeAlsa struct {
	mu               sync.RWMutex
	AutoMuteDisabled map[string]bool
	ControlVolumes   map[string]int
	ControlMutes     map[string]bool
	Calls            []string
	AutoMuteErr      error
	SetControlErr    error
}

var _ adapters.AlsaPort = (*FakeAlsa)(nil)

// NewFakeAlsa constructs a clean fake ALSA adapter.
func NewFakeAlsa() *FakeAlsa {
	return &FakeAlsa{
		AutoMuteDisabled: make(map[string]bool),
		ControlVolumes:   make(map[string]int),
		ControlMutes:     make(map[string]bool),
	}
}

func (f *FakeAlsa) EnsureAutoMuteDisabled(ctx context.Context, card string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "EnsureAutoMuteDisabled:"+card)
	if f.AutoMuteErr != nil {
		return f.AutoMuteErr
	}
	f.AutoMuteDisabled[card] = true
	return nil
}

func (f *FakeAlsa) SetControl(ctx context.Context, card string, control string, volumePercent int, muted bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fmt.Sprintf("%s:%s", card, control)
	f.Calls = append(f.Calls, fmt.Sprintf("SetControl:%s:%d:%t", key, volumePercent, muted))
	if f.SetControlErr != nil {
		return f.SetControlErr
	}
	f.ControlVolumes[key] = volumePercent
	f.ControlMutes[key] = muted
	return nil
}
