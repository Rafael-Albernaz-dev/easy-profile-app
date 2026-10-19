package easyeffects

import (
	"context"
	"sync"

	"github.com/toru/easy-profile-app/internal/adapters"
)

// FakeEasyEffects is an in-memory test double implementing adapters.EasyEffectsPort.
type FakeEasyEffects struct {
	mu               sync.RWMutex
	ActivePreset     string
	AvailablePresets []string
	Calls            []string
	LoadPresetErr    error
	GetActiveErr     error
	ListErr          error
	Devices          adapters.DSPDevices
	DevicesErr       error
	EnsureDevicesErr error
}

var _ adapters.EasyEffectsPort = (*FakeEasyEffects)(nil)

// NewFakeEasyEffects constructs a clean fake EasyEffects adapter with initial presets.
func NewFakeEasyEffects(initialPresets ...string) *FakeEasyEffects {
	if len(initialPresets) == 0 {
		initialPresets = []string{
			"AKG K52 - Uso diario aprovado",
			"AKG K52 - Rock e Metal",
			"Multi SP166 - Caixas 2.1 aprovado",
		}
	}
	return &FakeEasyEffects{
		Devices:          adapters.DSPDevices{Input: "alsa_input.usb-MU900_KA-1333_20190805V001-00.iec958-stereo.3", Output: "alsa_output.pci-0000_00_1f.3.analog-stereo"},
		ActivePreset:     initialPresets[0],
		AvailablePresets: initialPresets,
	}
}

func (f *FakeEasyEffects) ReadDevices(ctx context.Context) (adapters.DSPDevices, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.Devices, f.DevicesErr
}

func (f *FakeEasyEffects) EnsureDevices(ctx context.Context, output, input string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.EnsureDevicesErr != nil {
		return f.EnsureDevicesErr
	}
	f.Devices = adapters.DSPDevices{Input: input, Output: output}
	f.Calls = append(f.Calls, "EnsureDevices:"+input+":"+output)
	return nil
}

func (f *FakeEasyEffects) LoadPreset(ctx context.Context, presetName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "LoadPreset:"+presetName)
	if f.LoadPresetErr != nil {
		return f.LoadPresetErr
	}
	f.ActivePreset = presetName
	return nil
}

func (f *FakeEasyEffects) GetActivePreset(ctx context.Context) (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.GetActiveErr != nil {
		return "", f.GetActiveErr
	}
	return f.ActivePreset, nil
}

func (f *FakeEasyEffects) ListAvailablePresets(ctx context.Context) ([]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	res := make([]string, len(f.AvailablePresets))
	copy(res, f.AvailablePresets)
	return res, nil
}
