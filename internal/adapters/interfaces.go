package adapters

import (
	"context"
)

// GraphState is a fresh readback of the endpoints needed to keep the DSP chain connected.
type GraphState struct {
	PhysicalMicrophone   string
	DefaultSink          string
	ConfiguredSink       string
	SinkMonitorVolumes   bool
	SourceMonitorVolumes bool
}

type DSPDevices struct {
	Input, Output                     string
	UseDefaultInput, UseDefaultOutput bool
}

// PipeWirePort defines the audio graph mutation and inspection capabilities required by the daemon.
type PipeWirePort interface {
	ReadGraphState(ctx context.Context, sink, source, physicalSink, micPrefix string) (GraphState, error)
	EnsureDefaultSink(ctx context.Context, sink string) error
	// EnsureMonitorChannelVolumes activates [ "monitor.channel-volumes", true ] on virtual null-sinks.
	EnsureMonitorChannelVolumes(ctx context.Context, nodeName string) error

	// SetNodeVolume sets the linear gain on a node by name or ID.
	SetNodeVolume(ctx context.Context, nodeName string, linearVolume float64) error

	// SetNodeMute updates the mute state on a node by name or ID.
	SetNodeMute(ctx context.Context, nodeName string, muted bool) error
	GetNodeMute(ctx context.Context, nodeName string) (bool, error)
	GetSinkPort(ctx context.Context, sinkName string) (string, error)
	MigrateRawMicStream(ctx context.Context, micPrefix, virtualName, application string) error

	// SetSinkPort switches the active analog port on the physical sink (e.g. analog-output-headphones vs analog-output-lineout).
	SetSinkPort(ctx context.Context, sinkName string, portName string) error

	// GetRawMicStreams inspects the PipeWire graph and returns client app names connected directly to the physical mic.
	GetRawMicStreams(ctx context.Context, micNodeName string) ([]string, error)
}

// AlsaPort defines the hardware mixer controls for the integrated sound card.
type AlsaPort interface {
	// EnsureAutoMuteDisabled ensures 'Auto-Mute Mode' is set to Disabled so front and rear ports don't fight.
	EnsureAutoMuteDisabled(ctx context.Context, card string) error

	// SetControl configures a mixer control (e.g. Headphone or Front) with an integer volume percentage and mute flag.
	SetControl(ctx context.Context, card string, control string, volumePercent int, muted bool) error
}

// EasyEffectsPort defines the preset management capabilities for EasyEffects.
type EasyEffectsPort interface {
	ReadDevices(ctx context.Context) (DSPDevices, error)
	EnsureDevices(ctx context.Context, output, input string) error
	// LoadPreset applies an output preset by name.
	LoadPreset(ctx context.Context, presetName string) error

	// GetActivePreset returns the currently loaded output preset.
	GetActivePreset(ctx context.Context) (string, error)

	// ListAvailablePresets returns all valid output preset names found in the config directory.
	ListAvailablePresets(ctx context.Context) ([]string, error)
}
