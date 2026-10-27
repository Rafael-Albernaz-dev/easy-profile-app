package pipewire

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/toru/easy-profile-app/internal/inspect"
)

func TestSharingNegotiatesMonoBeforeLoopbackCopy(t *testing.T) {
	// Independent backend contract: PipeWire 1.0.5 module-loopback.c
	// playback_process copies data planes by index, without downmixing.
	// Both ends must negotiate MONO; the capture adapter does the remix.
	// This verifies the requested format, not DSP or remote-call reception.
	captureJSON, playbackJSON := sharingProperties("physical-output")
	decode := func(raw string) map[string]any {
		t.Helper()
		var props map[string]any
		if err := json.Unmarshal([]byte(raw), &props); err != nil {
			t.Fatal(err)
		}
		return props
	}
	capture, playback := decode(captureJSON), decode(playbackJSON)
	for _, props := range []map[string]any{capture, playback} {
		channels, ok := props["audio.position"].([]any)
		if props["audio.channels"] != float64(1) || !ok || len(channels) != 1 || channels[0] != "MONO" {
			t.Fatalf("loopback would copy incompatible channel layouts: %+v", props)
		}
	}
	if capture["stream.dont-remix"] != false {
		t.Fatal("physical stereo monitor must be remixed before the mono loopback")
	}
	if capture["target.object"] != "physical-output" || capture["stream.capture.sink"] != true || capture["node.passive"] != true || capture["node.dont-fallback"] != true || capture["node.dont-reconnect"] != true {
		t.Fatalf("monitor targeting/privacy contract changed: %+v", capture)
	}
	if playback["media.class"] != "Audio/Source" || playback["node.name"] != sharingSource || playback["node.autoconnect"] != false {
		t.Fatalf("virtual source must remain explicit and per-call: %+v", playback)
	}
}

type sharingRig struct {
	g                      *inspect.Graph
	r                      *RealPipeWire
	done                   chan struct{}
	failShare, failRestore bool
	starts                 int
}

func newSharingRig(t *testing.T) *sharingRig {
	t.Helper()
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	f := &sharingRig{done: make(chan struct{}), g: &inspect.Graph{
		Nodes: map[int]*inspect.NodeInfo{
			1: {ID: 1, Serial: 11, Name: "mic", MediaClass: "Audio/Source/Virtual"},
			2: {ID: 2, Serial: 22, Name: "call", MediaClass: "Stream/Input/Audio", AppName: "Browser"},
			3: {ID: 3, Serial: 33, Name: "output", MediaClass: "Audio/Sink"},
		},
		Links:          []inspect.LinkInfo{{OutNode: 1, InNode: 2, State: "active"}},
		CaptureTargets: map[int]inspect.TargetMetadata{},
	}}
	f.r = NewRealPipeWire(80 * time.Millisecond)
	f.r.sharingIO = &sharingIO{
		graph: func(context.Context) (*inspect.Graph, error) { return f.g, nil },
		start: func(string) (*sharingProcess, error) {
			f.starts++
			f.g.Nodes[4] = &inspect.NodeInfo{ID: 4, Serial: 44, Name: sharingSource, MediaClass: "Audio/Source"}
			f.g.Nodes[5] = &inspect.NodeInfo{ID: 5, Serial: 55, Name: "easyprofile_share_capture", MediaClass: "Stream/Input/Audio"}
			f.g.Links = append(f.g.Links, inspect.LinkInfo{OutNode: 3, InNode: 5, State: "active"})
			return &sharingProcess{done: f.done, stop: func(context.Context) error {
				select {
				case <-f.done:
				default:
					close(f.done)
				}
				delete(f.g.Nodes, 4)
				delete(f.g.Nodes, 5)
				links := f.g.Links[:0]
				for _, l := range f.g.Links {
					if l.OutNode != 4 && l.InNode != 5 {
						links = append(links, l)
					}
				}
				f.g.Links = links
				return nil
			}}, nil
		},
		target: func(_ context.Context, id int, target *inspect.TargetMetadata) error {
			if target == nil {
				delete(f.g.CaptureTargets, id)
			} else {
				f.g.CaptureTargets[id] = *target
			}
			out := 1
			sharing := target != nil && target.Value == "44"
			if sharing {
				out = 4
			}
			f.g.Links = []inspect.LinkInfo{{OutNode: out, InNode: id, State: "active"}}
			if f.g.Nodes[5] != nil {
				f.g.Links = append(f.g.Links, inspect.LinkInfo{OutNode: 3, InNode: 5, State: "active"})
			}
			if sharing && f.failShare {
				return errors.New("failed after applying target")
			}
			if !sharing && f.failRestore {
				return errors.New("restoration failed after applying target")
			}
			return nil
		},
	}
	return f
}

func TestSharingRoundTripPreservesOriginalPolicy(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "follow-default", true: "explicit-target"}[explicit], func(t *testing.T) {
			f := newSharingRig(t)
			prior := inspect.TargetMetadata{Value: "11", Type: "Spa:Id"}
			if explicit {
				f.g.CaptureTargets[2] = prior
			}
			if err := f.r.startOutputSharing(context.Background(), "output"); err != nil {
				t.Fatal(err)
			}
			state, err := f.r.readOutputSharing(context.Background())
			if err != nil || !state.Active || state.RestorePending || !exclusiveCapture(f.g, 2, sharingSource) {
				t.Fatalf("not exclusively sharing: %+v %v", state, err)
			}
			if err := f.r.stopOutputSharing(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !exclusiveCapture(f.g, 2, "mic") || f.r.sharing != nil {
				t.Fatal("original capture not restored")
			}
			got, ok := f.g.CaptureTargets[2]
			if ok != explicit || (ok && got != prior) {
				t.Fatalf("policy changed: %+v", f.g.CaptureTargets)
			}
		})
	}
}

func TestSharingRefusesAmbiguityAndMissingCapture(t *testing.T) {
	for _, mode := range []string{"missing", "multiple", "mixed", "reserved"} {
		t.Run(mode, func(t *testing.T) {
			f := newSharingRig(t)
			switch mode {
			case "missing":
				f.g.Links = nil
			case "multiple":
				f.g.Nodes[6] = &inspect.NodeInfo{ID: 6, Serial: 66, Name: "recorder", MediaClass: "Stream/Input/Audio"}
				f.g.Links = append(f.g.Links, inspect.LinkInfo{OutNode: 1, InNode: 6, State: "active"})
			case "mixed":
				f.g.Nodes[6] = &inspect.NodeInfo{ID: 6, Serial: 66, Name: "other-mic", MediaClass: "Audio/Source"}
				f.g.Links = append(f.g.Links, inspect.LinkInfo{OutNode: 6, InNode: 2, State: "active"})
			case "reserved":
				f.g.Nodes[6] = &inspect.NodeInfo{ID: 6, Serial: 66, Name: sharingSource}
			}
			if err := f.r.startOutputSharing(context.Background(), "output"); err == nil || f.starts != 0 {
				t.Fatal("unsafe capture accepted")
			}
		})
	}
}

func TestSharingActivationFailureRollsBackAppliedTarget(t *testing.T) {
	f := newSharingRig(t)
	f.failShare = true
	if err := f.r.startOutputSharing(context.Background(), "output"); err == nil {
		t.Fatal("failure hidden")
	}
	if f.r.sharing != nil || !exclusiveCapture(f.g, 2, "mic") {
		t.Fatal("partial activation not rolled back")
	}
	select {
	case <-f.done:
	default:
		t.Fatal("transport still alive")
	}
}

func TestSharingRestoreRetryAfterPartialFailure(t *testing.T) {
	f := newSharingRig(t)
	if err := f.r.startOutputSharing(context.Background(), "output"); err != nil {
		t.Fatal(err)
	}
	f.failRestore = true
	if err := f.r.stopOutputSharing(context.Background()); err == nil {
		t.Fatal("restore failure hidden")
	}
	state, _ := f.r.readOutputSharing(context.Background())
	if state.Active || !state.RestorePending {
		t.Fatalf("incorrect failure state: %+v", state)
	}
	f.failRestore = false
	if err := f.r.stopOutputSharing(context.Background()); err != nil || f.r.sharing != nil {
		t.Fatalf("retry failed: %v", err)
	}
}

func TestSharingNeverRestoresReusedStreamID(t *testing.T) {
	f := newSharingRig(t)
	if err := f.r.startOutputSharing(context.Background(), "output"); err != nil {
		t.Fatal(err)
	}
	f.g.Nodes[2].Serial = 999
	if err := f.r.stopOutputSharing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.g.CaptureTargets[2].Value != "44" {
		t.Fatal("reused stream identity was mutated")
	}
}

func TestSharingLostSourceStopsBeforeReportingRestoreFailure(t *testing.T) {
	f := newSharingRig(t)
	if err := f.r.startOutputSharing(context.Background(), "output"); err != nil {
		t.Fatal(err)
	}
	f.g.Nodes[1].Serial = 999
	if err := f.r.stopOutputSharing(context.Background()); err == nil {
		t.Fatal("missing original identity ignored")
	}
	select {
	case <-f.done:
	default:
		t.Fatal("transport not stopped")
	}
	if f.r.sharing == nil {
		t.Fatal("lost restoration snapshot")
	}
}

func TestSharingHonorsManualCaptureOverride(t *testing.T) {
	f := newSharingRig(t)
	if err := f.r.startOutputSharing(context.Background(), "output"); err != nil {
		t.Fatal(err)
	}
	f.g.CaptureTargets[2] = inspect.TargetMetadata{Value: "user-choice", Type: "Spa:String"}
	if err := f.r.stopOutputSharing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.g.CaptureTargets[2].Value != "user-choice" {
		t.Fatal("manual target overwritten")
	}
}

func TestSharingPublicAdapterBlocksHostInTestMode(t *testing.T) {
	f := newSharingRig(t)
	if err := f.r.StartOutputSharing(context.Background(), "output"); !errors.Is(err, ErrTestModeBlocked) {
		t.Fatal(err)
	}
	if err := f.r.StopOutputSharing(context.Background()); !errors.Is(err, ErrTestModeBlocked) {
		t.Fatal(err)
	}
	if _, err := f.r.ReadOutputSharing(context.Background()); !errors.Is(err, ErrTestModeBlocked) {
		t.Fatal(err)
	}
	if f.starts != 0 {
		t.Fatal("test mode reached transport")
	}
}

func TestSharingCancelledActivationStillStopsTransport(t *testing.T) {
	f := newSharingRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	start := f.r.sharingIO.start
	f.r.sharingIO.start = func(sink string) (*sharingProcess, error) {
		p, err := start(sink)
		delete(f.g.Nodes, 4) // readiness never arrives
		cancel()
		return p, err
	}
	if err := f.r.startOutputSharing(ctx, "output"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	select {
	case <-f.done:
	default:
		t.Fatal("cancelled transaction left transport alive")
	}
	if f.r.sharing != nil || !exclusiveCapture(f.g, 2, "mic") {
		t.Fatal("cancelled activation changed input")
	}
}

func TestSharingProcessDeathIsPendingUntilRestoration(t *testing.T) {
	f := newSharingRig(t)
	if err := f.r.startOutputSharing(context.Background(), "output"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.sharing.process.stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := f.r.readOutputSharing(context.Background())
	if err == nil || state.Active || !state.RestorePending {
		t.Fatalf("death hidden: %+v %v", state, err)
	}
	if err := f.r.stopOutputSharing(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !exclusiveCapture(f.g, 2, "mic") {
		t.Fatal("original route lost after timeout/death")
	}
}

func TestSharingReadbackRejectsMixedMicrophoneAndMissingFeed(t *testing.T) {
	for _, mixed := range []bool{true, false} {
		f := newSharingRig(t)
		if err := f.r.startOutputSharing(context.Background(), "output"); err != nil {
			t.Fatal(err)
		}
		if mixed {
			f.g.Links = append(f.g.Links, inspect.LinkInfo{OutNode: 1, InNode: 2, State: "active"})
		} else {
			f.g.Links = f.g.Links[:1]
		}
		state, err := f.r.readOutputSharing(context.Background())
		if err == nil || state.Active || !state.RestorePending {
			t.Fatalf("unconfirmed route hidden: %+v %v", state, err)
		}
	}
}

func TestSharingCandidateGranularDiagnostics(t *testing.T) {
	// Case 1: No capture stream at all
	g := &inspect.Graph{
		Nodes: map[int]*inspect.NodeInfo{
			1: {ID: 1, Serial: 11, Name: "mic", MediaClass: "Audio/Source/Virtual"},
			3: {ID: 3, Serial: 33, Name: "output", MediaClass: "Audio/Sink"},
		},
		Links: []inspect.LinkInfo{},
	}
	_, err := sharingCandidate(g, "output")
	if err == nil || !strings.Contains(err.Error(), "nenhuma chamada capturando o microfone") {
		t.Fatalf("expected no capture error, got: %v", err)
	}

	// Case 2: One capture stream, but link is paused
	g.Nodes[2] = &inspect.NodeInfo{ID: 2, Serial: 22, Name: "call", MediaClass: "Stream/Input/Audio"}
	g.Links = []inspect.LinkInfo{{OutNode: 1, InNode: 2, State: "paused"}}
	_, err = sharingCandidate(g, "output")
	if err == nil || !strings.Contains(err.Error(), "captura está pausada") {
		t.Fatalf("expected paused capture error, got: %v", err)
	}

	// Case 3: One capture stream, but unlinked
	g.Links = nil
	_, err = sharingCandidate(g, "output")
	if err == nil || !strings.Contains(err.Error(), "ainda não conectada") {
		t.Fatalf("expected unlinked capture error, got: %v", err)
	}

	// Case 4: Multiple capture streams
	g.Nodes[4] = &inspect.NodeInfo{ID: 4, Serial: 44, Name: "other-call", MediaClass: "Stream/Input/Audio"}
	g.Links = []inspect.LinkInfo{
		{OutNode: 1, InNode: 2, State: "active"},
		{OutNode: 1, InNode: 4, State: "active"},
	}
	_, err = sharingCandidate(g, "output")
	if err == nil || !strings.Contains(err.Error(), "múltiplas capturas de áudio detectadas") {
		t.Fatalf("expected multiple captures error, got: %v", err)
	}
	delete(g.Nodes, 4)

	// Case 5: Source is not Audio/Source
	g.Nodes[5] = &inspect.NodeInfo{ID: 5, Serial: 55, Name: "not-a-source", MediaClass: "Stream/Output/Audio"}
	g.Links = []inspect.LinkInfo{{OutNode: 5, InNode: 2, State: "active"}}
	_, err = sharingCandidate(g, "output")
	if err == nil || !strings.Contains(err.Error(), "fonte não reconhecida") {
		t.Fatalf("expected unrecognized source error, got: %v", err)
	}
	delete(g.Nodes, 5)

	// Case 6: Missing serial / permissions
	g.Nodes[2].Serial = 0
	g.Links = []inspect.LinkInfo{{OutNode: 1, InNode: 2, State: "active"}}
	_, err = sharingCandidate(g, "output")
	if err == nil || !strings.Contains(err.Error(), "identificador da captura indisponível") {
		t.Fatalf("expected missing serial error, got: %v", err)
	}
	g.Nodes[2].Serial = 22

	// Case 7: Valid active capture
	session, err := sharingCandidate(g, "output")
	if err != nil || session == nil || session.stream.ID != 2 || session.original.ID != 1 {
		t.Fatalf("expected valid candidate, got session=%v err=%v", session, err)
	}
}

func TestSharingPassiveFeedActivatesOnlyAfterConsumer(t *testing.T) {
	f := newSharingRig(t)
	start := f.r.sharingIO.start
	f.r.sharingIO.start = func(sink string) (*sharingProcess, error) {
		p, e := start(sink)
		f.g.Links[1].State = "paused"
		return p, e
	}
	if err := f.r.startOutputSharing(context.Background(), "output"); err != nil {
		t.Fatal(err)
	}
	if !sharingFeed(f.g, "output") {
		t.Fatal("transport not active")
	}
}
func TestSharingPausedFeedNeverReportsActive(t *testing.T) {
	f := newSharingRig(t)
	target := f.r.sharingIO.target
	f.r.sharingIO.target = func(ctx context.Context, id int, v *inspect.TargetMetadata) error {
		err := target(ctx, id, v)
		if v != nil && v.Value == "44" {
			f.g.Links[1].State = "paused"
		}
		return err
	}
	if err := f.r.startOutputSharing(context.Background(), "output"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if f.r.sharing != nil {
		t.Fatal("failed transport not cleaned")
	}
}
