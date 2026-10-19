# EasyProfileApp 🎧⚡

Personal audio controller for Ubuntu 24.04 / GNOME Shell 46 Wayland, PipeWire 1.0.5, WirePlumber 0.4.17 and EasyEffects 7.1.6. One Go CLI/daemon and a native GJS extension communicate through Session D-Bus (`org.easyprofile.Daemon`).

The calibrated setup uses AKG K52 front headphones, SP166 rear speakers and the KA-1333 USB microphone. Hardware identities, output routes and preset compatibility are configured in `config.example.json`.

## Safety and behavior

- Output route changes mute the physical sink and both ALSA pins before switching. The inactive pin stays muted; the active route receives its calibrated physical ceiling (AKG ≤97%, SP166 ≤100%). Hardware is released only after readbacks succeed.
- Mic level changes affect the post-DSP source; physical USB gain is untouched. Dual-mute acts independently on the virtual and physical sources, with readback and degraded-state reporting.
- Panic saves three independent mute booleans. Manual mute overrides update the corresponding saved values. Daily reset preserves the two current microphone mute states.
- Volumes and compatible presets are remembered per destination and graphical login. Same-login restart restores them. Corrupt, incomplete or missing same-login state refuses automatic default gains/unmute.
- A process flock is acquired before session loading or hardware initialization. Login authorization is stored atomically before any automatic unmute. Login-marker and audio-state files are separate (`login_session.json` and `session.json`).
- Background reconciliation maintains the virtual default sink, EasyEffects input/output device selections and monitor gain, using fresh identity/readbacks. KA-1333 resolution checks USB identity/profile. Cached IDs are invalidated on graph changes and failed calls.
- Raw microphone captures are reported. Migration is explicit per application, rejects ambiguity and confirms processed links.
- Status reads use an immutable published snapshot, independently of slow hardware operations. UI level requests carry a destination, and route/reset barriers discard pending levels and wait for calls already sent.
- Microphone monitoring (post-DSP sidetone) creates dedicated PipeWire links from `easyeffects_source` to `easyeffects_sink`. A blue headset panel icon indicates active monitoring, and enabling monitoring automatically un-mutes the microphone if muted.

Automated tests use in-memory audio adapters with `EASYPROFILE_TEST_MODE=1`. No test may control real audio. Production guards and the three calibrated preset files remain untouched during development. Passing local gates does not certify physical routing, sound quality or GNOME Shell behavior.

## Official 17-key keypad contract

| Key | Action | Repeat |
|---|---|---|
| `NumLock` | Audio mode on/off (`⚡`); off restores normal keypad behavior | Native toggle |
| `+` | Focused output/mic volume +1%, clamped to 100% | Yes |
| `-` | Focused output/mic volume −1%, clamped to 0% | Yes |
| `1` | Select AKG K52; focus output | No |
| `2` | Select SP166; focus output | No |
| `3` | Toggle volume focus between output and microphone | No |
| `4` | Select compatible preset 1 for the active output | No |
| `5` | Select compatible preset 2 for the active output | No |
| `6` | Select compatible preset 3 for the active output | No |
| `0` | Microphone dual-mute; red panel icon indicates mic mute state | No |
| `.` / `,` | Output mute (`KP_Decimal` / `KP_Separator`) | No |
| `Enter` | Panic: output and microphone mute; toggle restores independent snapshot | No |
| `/` | DynamicEqualizer placeholder; logs intent, no DSP change | No |
| `*` | Daily reset: base preset, output 95%, mic 100%; preserves both mic mutes | No |
| `7` | Toggle post-DSP microphone monitoring (sidetone); blue headset panel icon; auto-unmutes mic | No |
| `8` | Reserved, silent no-op | No |
| `9` | Reserved, silent no-op | No |

NumLock is monitored natively; the other 16 bindings are registered only in audio mode. Only `+`/`-` allow autorepeat. Routine actions produce no OSD; real errors do. Volume labels update locally while confirmation is pending. Icons/mute claims use daemon state rather than assuming hardware success. Restart is available from the menu through systemd only; `*` does not restart the daemon.

## Architecture

```mermaid
flowchart LR
  UI[GNOME 46 GJS] <--> BUS[Session D-Bus]
  CLI[easyprofilectl] <--> BUS
  BUS <--> DAEMON[Go serialized worker / published status]
  DAEMON --> DOMAIN[Controller / private session state]
  DAEMON --> PW[PipeWire adapter]
  DAEMON --> ALSA[ALSA adapter]
  DAEMON --> EE[EasyEffects adapter]
```

No HTTP/WebSocket server or second IPC exists. Interfaces describe operations actually used. The controller protects domain invariants; the daemon sequences hardware operations. Session writes are synchronous and atomic, outside the status-read path, and I/O failures are reported.

## Build and isolated verification

Requires Go **1.27.1**, Node.js, GJS, staticcheck, D-Bus tools and GNOME extension packaging/schema tools.

```bash
make build extension-pack
EASYPROFILE_TEST_MODE=1 GSETTINGS_BACKEND=memory go test -race ./...
make quality-gate
```

The six gates cover formatting/vet/staticcheck/build; Go AST subprocess/file-write rules and private Git hygiene; preset/guard integrity and extension packaging; race tests, fake cutover failures and extension lifecycle/concurrency tests; CLI/daemon/shared-GJS-proxy integration on a private bus with no activation directories; captured-dump parse and isolated fake-process performance.

Measured budgets: parser <5 ms/op; direct status read during blocked hardware <5 ms; fake CLI status p95 <15 ms over 100 samples; fake daemon RSS <15 MB; fake idle CPU <1% of one core over 60 seconds. CLI maximum is reported separately. Real adapters, child processes and GNOME still require accompanied measurement. No aggressive CPU quota is claimed or imposed.

## Observation and CLI

**Always pass `--observe` for a daemon run without hardware control.** The default is control mode; omitting this flag is not a read-only test.

```bash
# Read-only audio observation; do not run alongside an existing daemon owning the lock.
bin/easyprofilectl daemon --observe --config config.example.json

# Read-only inspection without starting a daemon
bin/easyprofilectl inspect

# Query the existing daemon
bin/easyprofilectl status --json
```

Control-mode examples below mutate audio and belong only to explicitly accompanied use:

```bash
easyprofilectl volume +1
easyprofilectl mic -1
easyprofilectl mute mic
easyprofilectl mute out
easyprofilectl panic
easyprofilectl listen toggle
easyprofilectl route akg
easyprofilectl route sp166
easyprofilectl preset list
easyprofilectl preset "AKG - Rock"
easyprofilectl reset
```

The systemd daemon requires a trustworthy graphical identity (`XDG_SESSION_ID` or `SESSION_MANAGER`) in its environment; stable Wayland/bus socket paths cannot authorize login unmute. `XDG_RUNTIME_DIR` supplies the process lock and separate state files. Lifecycle/configuration methods are not exported on D-Bus. The UI uses `SetVolumeForDestination`; stale destinations are rejected inside the worker.

## Installation and rollback

Development does not authorize executing a real cutover. The installer and rollback are prepared for a separately authorized production session:

- `scripts/install.sh --dry-run` reads hashes and service states without builds, writes or snapshot replacement.
- A real install atomically creates an immutable rollback point. Existing snapshots cause refusal before installation; never delete one blindly to retry.
- An already active EasyProfile service is preserved and causes refusal before installation; this script is not a hot upgrade path.
- After legacy guard deactivation begins, catchable errors/signals trigger rollback. Readiness requires structured JSON health with confirmed protection, no error, DSP bindings and monitor gain confirmed. A received degraded/invalid reply rolls back immediately; only unavailable transport/readiness is retried within a deadline. SIGKILL/power loss cannot execute a shell trap; retain the snapshot for explicit recovery.
- Rollback removes the D-Bus activation file, stops the new daemon, restores each saved active/enabled state symmetrically and verifies all three preset hashes. Missing/corrupt snapshots are errors; there is no guessed enable/start fallback.

See [the current handoff](docs/EP-004-EP-005-HANDOFF.md) and [architecture requirements](ARCHITECTURE_AND_REQUIREMENTS.md) for validation boundaries and the accompanied QA checklist.

## License

MIT © Rafael Albernaz.
