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
	DefaultSink           string
	ConfiguredSink        string
	PhysicalMicrophone    string
	GraphErr              error
	DefaultErr            error
	MicMonitoring         bool
}

var _ adapters.PipeWirePort = (*FakePipeWire)(nil)

// NewFakePipeWire constructs a clean fake PipeWire adapter.
func NewFakePipeWire() *FakePipeWire {
	return &FakePipeWire{
		MonitorChannelVolumes: map[string]bool{"easyeffects_sink": true, "easyeffects_source": true},
		DefaultSink:           "easyeffects_sink",
		ConfiguredSink:        "easyeffects_sink",
		PhysicalMicrophone:    "alsa_input.usb-MU900_KA-1333_20190805V001-00.iec958-stereo.3",
		NodeVolumes:           make(map[string]float64),
		NodeMutes:             make(map[string]bool),
		SinkPorts:             make(map[string]string),
	}
}

func (f *FakePipeWire) ReadGraphState(ctx context.Context, sink, source, physicalSink, micPrefix string) (adapters.GraphState, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return adapters.GraphState{PhysicalMicrophone: f.PhysicalMicrophone, DefaultSink: f.DefaultSink, ConfiguredSink: f.ConfiguredSink,
		SinkMonitorVolumes: f.MonitorChannelVolumes[sink], SourceMonitorVolumes: f.MonitorChannelVolumes[source]}, f.GraphErr
}

func (f *FakePipeWire) EnsureDefaultSink(ctx context.Context, sink string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DefaultErr != nil {
		return f.DefaultErr
	}
	f.DefaultSink, f.ConfiguredSink = sink, sink
	f.Calls = append(f.Calls, "EnsureDefaultSink:"+sink)
	return nil
}

func (f *FakePipeWire) EnsureDefaultSource(ctx context.Context, source string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DefaultErr != nil {
		return f.DefaultErr
	}
	f.Calls = append(f.Calls, "EnsureDefaultSource:"+source)
	return nil
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

func (f *FakePipeWire) SetMicMonitoring(ctx context.Context, sourceNode, sinkNode string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MicMonitoring = enabled
	f.Calls = append(f.Calls, fmt.Sprintf("SetMicMonitoring:%v", enabled))
	return nil
}

func (f *FakePipeWire) GetMicMonitoring(ctx context.Context, sourceNode, sinkNode string) (bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.MicMonitoring, nil
}
