# System-default audio through EasyEffects

Status: audited design; existing defaults are configured, but continuous input-default validation is not implemented yet. No browser settings or live audio routes were changed by this audit.

## What “Default” should mean

A browser using the system default should receive the processed virtual microphone and send playback through the processed virtual output:

```mermaid
flowchart LR
  MIC[Physical microphone] --> IN[EasyEffects input filters]
  IN --> SOURCE[easyeffects_source: default input]
  SOURCE --> CALL[Browser / call using Default]
  CALL --> SINK[easyeffects_sink: default output]
  SINK --> OUT[EasyEffects output filters]
  OUT --> HW[Selected physical headphones or speakers]
```

The OS default is a selection of an existing endpoint, not another DSP chain. No extra virtual device, loopback or preset rewrite is required. Friendly names would help users recognize the endpoints but do not establish the routing policy.

The relevant PipeWire metadata must agree:

| Metadata key | Expected endpoint |
|---|---|
| `default.audio.source` | `easyeffects_source` |
| `default.configured.audio.source` | `easyeffects_source` |
| `default.audio.sink` | `easyeffects_sink` |
| `default.configured.audio.sink` | `easyeffects_sink` |

Configured defaults record the requested preference; effective defaults describe the current selection. Seeing only a successful write or configured value is insufficient proof that the selected endpoint is effective.

EasyEffects itself must use the physical KA-1333 input and the selected physical output explicitly, rather than follow its own virtual defaults. This avoids routing the processed endpoints back into themselves. The daemon already enforces those explicit EasyEffects device selections.

## Current code and the missing protection

`internal/daemon/daemon.go: ensureDSPBindings` calls both `EnsureDefaultSink` and `EnsureDefaultSource` during startup and protected destination application. `internal/adapters/pipewire/graph_state.go` implements those operations.

The sink operation confirms both metadata values with a fresh graph. The source operation currently writes the configured source but does not verify the effective result afterwards. `GraphState` and the daemon's `DSPBound` check currently include only the two sink-default values. An external change affecting only the input default can therefore escape background repair. Current `dsp_bound=true` must not be interpreted as confirmation of all four defaults.

The proposed next implementation is:

1. Add effective/configured input defaults to the existing fresh graph observation and require all four default values in DSP binding validation. Use the consolidated snapshot; do not add continuous polling or a second parser.
2. Make the source-default setter confirm both input metadata values after selection, with a bounded deadline for WirePlumber convergence. Missing nodes, unknown observations or a timeout must report degraded protection, never successful binding.
3. Use the existing serialized, protected repair sequence on default drift and node recreation. Preserve all mute states, session levels and presets; background repair must never authorize a new microphone unmute. Cover source-only drift, configured/effective disagreement, failed repair, startup, node recreation and healthy no-op reconciliation with fakes.

Friendly descriptions such as “EasyProfile — Processed microphone” and “EasyProfile — Processed output” are optional follow-up work. Stable `node.name` identifiers must remain unchanged. Renaming requires checking propagation through PipeWire-Pulse/browser enumeration and restoration after node recreation.

## Browser and application choices

[Chrome](https://support.google.com/chrome/answer/2693767?hl=en) exposes a browser default microphone. [Google Meet](https://support.google.com/meet/answer/10409699?hl=en) also lets the user select the microphone and speaker before or during a call. Neither selection should be confused with permission to access the microphone.

When the application follows the OS default, making the processed virtual nodes the defaults is the intended solution. If the browser exposes a “Default / System default” choice, use it. If it has retained a physical device explicitly, select the EasyEffects processed device or clear that explicit choice once. OS default selection alone cannot promise to override every saved application choice.

The [WirePlumber 0.4.17 linking policy](https://github.com/PipeWire/wireplumber/blob/0.4.17/src/scripts/policy-node.lua) handles explicitly requested targets as well as default endpoints. Automatically moving every pinned stream would be a separate policy, not simply changing the system default. The current app reports raw microphone captures and offers explicit per-application migration. It does not implement forced migration of every browser input/output stream.

WhatsApp Web must be tested independently in the installed browser; no universal automatic device-selection behavior is assumed. Changing an OS default may require reopening a capture/call depending on how the application opened its stream.

## Human acceptance checklist

After implementing the proposed protection, validate on the actual desktop:

- Fresh login with EasyEffects endpoints present, and startup where they appear later.
- Chrome and Meet using the system default: confirm both processed capture and processed playback links, not just the device label or a moving level meter.
- WhatsApp Web with a new call and an already-open call; inspect actual capture and playback links separately.
- External changes to each of the four default values and recreation of the virtual nodes; confirm bounded repair and truthful degraded states on failure.
- Explicit physical-device choices remain distinguishable from default-following streams; migration stays intentional.
- Muted microphone, panic, output sharing and preset changes retain their privacy/routing contracts. Preset hashes remain unchanged.

This checklist is pending physical acceptance; documentation, fake tests and metadata alone do not certify what a remote participant hears.
