# System-default audio through EasyEffects

Status: implemented with hermetic tests; physical browser/call acceptance is pending. No browser settings or live audio routes were changed during development.

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

## Implementation and safety contract

`internal/adapters/pipewire/graph_state.go` includes the effective and configured defaults for both input and output in the existing fresh graph observation. `internal/daemon/daemon.go` requires all four values, explicit physical EasyEffects device selections and resolved endpoints before reporting `dsp_bound=true`.

`internal/adapters/pipewire/defaults.go` selects the defaults and confirms both values afterwards. A healthy selection uses one read and performs no writes. Repairs use fresh snapshots, retrying convergence at 50 ms intervals within the adapter's configured timeout (3 seconds by default), also respecting parent cancellation. Missing/ambiguous/wrong-class endpoints, read failures, failed writes or an expired deadline cannot be reported as confirmed. Sink selection uses the freshly resolved node ID rather than a cached ID. Source metadata is JSON-encoded with explicit `Spa:String:JSON` type, following the [PipeWire metadata CLI](https://docs.pipewire.org/page_man_pw-metadata_1.html).

`internal/adapters/pipewire/watch.go` wakes the existing reconciler for effective/configured default metadata changes and deletions, including changes without any Node/Link/Device event. Default-metadata creation/removal and graph/cache invalidation also remain wakeups. Events are coalesced; deltas never replace a fresh observation. There is no new polling loop while defaults are healthy.

Default drift uses the existing serialized, protected destination repair. It preserves session levels, presets, panic and independent microphone mute states. It never authorizes a new microphone unmute in the background. A failed repair keeps physical output muted and protection unconfirmed. A partial microphone mute remains visible as `degraded_partial_mute`; successful DSP binding does not hide that privacy state. Observe-only mode reports drift without changing defaults.

Friendly descriptions such as “EasyProfile — Processed microphone” and “EasyProfile — Processed output” are optional follow-up work. Stable `node.name` identifiers must remain unchanged. Renaming requires checking propagation through PipeWire-Pulse/browser enumeration and restoration after node recreation.

## Browser and application choices

[Chrome](https://support.google.com/chrome/answer/2693767?hl=en) exposes a browser default microphone. [Google Meet](https://support.google.com/meet/answer/10409699?hl=en) also lets the user select the microphone and speaker before or during a call. Neither selection should be confused with permission to access the microphone.

When the application follows the OS default, making the processed virtual nodes the defaults is the intended solution. If the browser exposes a “Default / System default” choice, use it. If it has retained a physical device explicitly, select the EasyEffects processed device or clear that explicit choice once. OS default selection alone cannot promise to override every saved application choice.

The [WirePlumber 0.4.17 linking policy](https://github.com/PipeWire/wireplumber/blob/0.4.17/src/scripts/policy-node.lua) handles explicitly requested targets as well as default endpoints. Automatically moving every pinned stream would be a separate policy, not simply changing the system default. The current app reports raw microphone captures and offers explicit per-application migration. It does not implement forced migration of every browser input/output stream.

WhatsApp Web must be tested independently in the installed browser; no universal automatic device-selection behavior is assumed. Changing an OS default may require reopening a capture/call depending on how the application opened its stream.

## Human acceptance checklist

With the new binary loaded, validate on the actual desktop:

- Fresh login with EasyEffects endpoints present, and startup where they appear later.
- Chrome and Meet using the system default: confirm both processed capture and processed playback links, not just the device label or a moving level meter.
- WhatsApp Web with a new call and an already-open call; inspect actual capture and playback links separately.
- External changes to each of the four default values and recreation of the virtual nodes; confirm bounded repair and truthful degraded states on failure.
- Explicit physical-device choices remain distinguishable from default-following streams; migration stays intentional.
- Muted microphone, panic, output sharing and preset changes retain their privacy/routing contracts. Preset hashes remain unchanged.

This checklist is pending physical acceptance; documentation, fake tests and metadata alone do not certify what a remote participant hears.

## Verification for this slice

Hermetic coverage includes all four default drifts; both directions of configured/effective disagreement; asynchronous convergence, deadline and cancellation; missing, invalid and ambiguous nodes; selection/read errors; metadata-only changes and deletion; watcher cache invalidation; observe-only behavior; failed readback after a successful setter; independent microphone mutes; session volume/preset preservation; panic and recovery after endpoint recreation. The existing healthy-reconciliation test still requires one observation and zero hardware writes.

Live read-only inspection confirmed all four defaults already point to the EasyEffects endpoints and that their media classes match the adapter contract. This does not prove behavior in a real call or recovery from an injected live fault. The prior full-gate limitations remain: legacy-guard expectations after cutover and the recorded fake CLI latency budget. No live fault was injected and no audio service was restarted during development.
