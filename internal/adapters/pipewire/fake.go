package pipewire

import (
	"context"
	"fmt"
	"sync"

	"github.com/toru/easy-profile-app/internal/adapters"
)

// FakePipeWire is an in-memory test double implementing adapters.PipeWirePort.
type FakePipeWire struct {
	mu                    sync.RWMutex
	MonitorChannelVolumes map[string]bool
	NodeVolumes           map[string]float64
	NodeMutes             map[string]bool
	SinkPorts             map[string]string
	RawStreams            []string
	Calls                 []string
	EnsureMonitorErr      error
	SetVolumeErr          error
	SetMuteErr            error
	SetPortErr            error
}

var _ adapters.PipeWirePort = (*FakePipeWire)(nil)

// NewFakePipeWire constructs a clean fake PipeWire adapter.
func NewFakePipeWire() *FakePipeWire {
	return &FakePipeWire{
		MonitorChannelVolumes: make(map[string]bool),
		NodeVolumes:           make(map[string]float64),
		NodeMutes:             make(map[string]bool),
		SinkPorts:             make(map[string]string),
	}
}

func (f *FakePipeWire) EnsureMonitorChannelVolumes(ctx context.Context, nodeName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "EnsureMonitorChannelVolumes:"+nodeName)
	if f.EnsureMonitorErr != nil {
		return f.EnsureMonitorErr
	}
	f.MonitorChannelVolumes[nodeName] = true
	return nil
}

func (f *FakePipeWire) SetNodeVolume(ctx context.Context, nodeName string, linearVolume float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "SetNodeVolume:"+nodeName)
	if f.SetVolumeErr != nil {
		return f.SetVolumeErr
	}
	f.NodeVolumes[nodeName] = linearVolume
	return nil
}

func (f *FakePipeWire) SetNodeMute(ctx context.Context, nodeName string, muted bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "SetNodeMute:"+nodeName)
	if f.SetMuteErr != nil {
		return f.SetMuteErr
	}
	f.NodeMutes[nodeName] = muted
	return nil
}

func (f *FakePipeWire) SetSinkPort(ctx context.Context, sinkName string, portName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "SetSinkPort:"+sinkName+":"+portName)
	if f.SetPortErr != nil {
		return f.SetPortErr
	}
	f.SinkPorts[sinkName] = portName
	return nil
}

func (f *FakePipeWire) GetRawMicStreams(ctx context.Context, micNodeName string) ([]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	res := make([]string, len(f.RawStreams))
	copy(res, f.RawStreams)
	return res, nil
}

func (f *FakePipeWire) GetNodeMute(ctx context.Context, name string) (bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.NodeMutes[name], nil
}
func (f *FakePipeWire) GetSinkPort(ctx context.Context, name string) (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if route := f.SinkPorts[name]; route != "" {
		return route, nil
	}
	return "analog-output-headphones", nil
}

func (f *FakePipeWire) MigrateRawMicStream(ctx context.Context, micPrefix, virtualName, application string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, name := range f.RawStreams {
		if name == application {
			f.RawStreams = append(f.RawStreams[:i], f.RawStreams[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("raw stream not found: %s", application)
}
