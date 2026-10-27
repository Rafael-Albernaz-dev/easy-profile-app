package pipewire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/inspect"
)

const sharingSource = "easyprofile_share_source"

type sharingProcess struct {
	done <-chan struct{}
	stop func(context.Context) error
}

// The same transaction is exercised with an in-memory graph in tests. No test
// launches a process or connects to PipeWire.
type sharingIO struct {
	graph  func(context.Context) (*inspect.Graph, error)
	target func(context.Context, int, *inspect.TargetMetadata) error
	start  func(string) (*sharingProcess, error)
}

type sharingSession struct {
	stream, original inspect.NodeInfo
	previous         *inspect.TargetMetadata
	process          *sharingProcess
	changed          bool
	restoring        bool
	sink             string
	sharedSerial     int
}

func (r *RealPipeWire) shareBackend() *sharingIO {
	if r.sharingIO != nil {
		return r.sharingIO
	}
	return &sharingIO{graph: r.graph, target: setCaptureTarget, start: startSharingProcess}
}

func setCaptureTarget(ctx context.Context, id int, target *inspect.TargetMetadata) error {
	args := []string{"-n", "default"}
	if target == nil {
		args = append(args, "-d", strconv.Itoa(id), "target.object")
	} else {
		// pw-metadata takes VALUE before TYPE (including on PipeWire 1.0.5).
		args = append(args, strconv.Itoa(id), "target.object", target.Value)
		if target.Type != "" {
			args = append(args, target.Type)
		}
	}
	if out, err := exec.CommandContext(ctx, "pw-metadata", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("capture target: %w: %s", err, out)
	}
	return nil
}

func sharingProperties(sink string) (string, string) {
	capture, _ := json.Marshal(map[string]any{
		"target.object": sink, "stream.capture.sink": true,
		"node.name": "easyprofile_share_capture", "node.dont-fallback": true,
		"node.dont-reconnect": true, "node.passive": true,
		// PipeWire 1.0.x loopback copies channels by index; stereo -> mono
		// here would discard FR. Negotiate MONO on both ends so the capture
		// stream's channel mixer combines the physical monitor's FL and FR.
		"audio.position": []string{"MONO"}, "audio.channels": 1,
		"stream.dont-remix": false,
	})
	playback, _ := json.Marshal(map[string]any{
		"node.name": sharingSource, "node.description": "EasyProfile — Compartilhar saída",
		"media.class": "Audio/Source", "node.virtual": true,
		"node.autoconnect": false,
		"audio.position":   []string{"MONO"}, "audio.channels": 1,
	})
	return string(capture), string(playback)
}

func startSharingProcess(sink string) (*sharingProcess, error) {
	capture, playback := sharingProperties(sink)
	// A sharing session is transient and bounded. It never survives daemon death
	// and stops after ten minutes even if the UI disappears.
	life, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	cmd := exec.CommandContext(life, "pw-loopback", "--capture-props="+capture, "--playback-props="+playback)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); cancel(); close(done) }()
	return &sharingProcess{done: done, stop: func(ctx context.Context) error {
		cancel()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}, nil
}

func sharingCandidate(g *inspect.Graph, sink string) (*sharingSession, error) {
	var sinkFound bool
	for _, n := range g.Nodes {
		if n.Name == sharingSource {
			return nil, errors.New("fonte de compartilhamento já em execução; desative o compartilhamento anterior primeiro")
		}
		if n.Name == sink && n.MediaClass == "Audio/Sink" {
			sinkFound = true
		}
	}
	if !sinkFound {
		return nil, errors.New("saída de áudio selecionada indisponível")
	}

	var captureStreams []*inspect.NodeInfo
	for _, n := range g.Nodes {
		if n == nil || n.MediaClass != "Stream/Input/Audio" {
			continue
		}
		if strings.HasPrefix(n.Name, "ee_") || strings.Contains(strings.ToLower(n.AppName), "easyeffects") {
			continue
		}
		if n.Name == "easyprofile_share_capture" {
			continue
		}
		captureStreams = append(captureStreams, n)
	}

	if len(captureStreams) == 0 {
		return nil, errors.New("nenhuma chamada capturando o microfone foi encontrada; abra a chamada antes de compartilhar")
	}
	if len(captureStreams) > 1 {
		return nil, errors.New("múltiplas capturas de áudio detectadas; mantenha apenas a chamada desejada capturando o microfone")
	}

	stream := captureStreams[0]
	if stream.Serial <= 0 {
		return nil, errors.New("identificador da captura indisponível; verifique as permissões e a conexão do PipeWire")
	}

	var streamLinks []inspect.LinkInfo
	var hasActiveLink, hasPausedLink bool
	for _, link := range g.Links {
		if link.InNode == stream.ID {
			streamLinks = append(streamLinks, link)
			if link.State == "active" {
				hasActiveLink = true
			} else if link.State == "paused" {
				hasPausedLink = true
			}
		}
	}

	if len(streamLinks) == 0 {
		return nil, errors.New("captura da chamada ainda não conectada ao áudio do sistema; aguarde a inicialização da chamada")
	}
	if !hasActiveLink {
		if hasPausedLink {
			return nil, errors.New("a chamada foi detectada, mas sua captura está pausada; ative a entrada de áudio na chamada e tente novamente")
		}
		return nil, errors.New("captura da chamada em estado inativo; desmute ou ative o áudio na chamada antes de compartilhar")
	}

	var selectedSource *inspect.NodeInfo
	for _, link := range streamLinks {
		if link.State != "active" {
			continue
		}
		source := g.Nodes[link.OutNode]
		if source == nil || !strings.HasPrefix(source.MediaClass, "Audio/Source") {
			return nil, errors.New("captura conectada a uma fonte não reconhecida; certifique-se de que a chamada está vinculada ao microfone")
		}
		if source.Serial <= 0 {
			return nil, errors.New("identificador da fonte indisponível ou permissões insuficientes no PipeWire")
		}
		if selectedSource != nil && selectedSource.ID != source.ID {
			return nil, errors.New("múltiplas fontes de áudio conectadas à chamada; mantenha apenas o microfone vinculado")
		}
		selectedSource = source
	}

	if selectedSource == nil {
		return nil, errors.New("nenhuma captura ativa do microfone confirmada; verifique a chamada antes de compartilhar")
	}

	session := &sharingSession{stream: *stream, original: *selectedSource}
	if target, ok := g.CaptureTargets[stream.ID]; ok {
		session.previous = &target
	}
	return session, nil
}

func sameSharingNode(g *inspect.Graph, saved inspect.NodeInfo) bool {
	n := g.Nodes[saved.ID]
	return n != nil && n.Serial == saved.Serial && n.Name == saved.Name
}

// exclusiveCapture checks every link, not just the existence of one desired
// edge: microphone audio must not remain mixed with the shared output.
func exclusiveCapture(g *inspect.Graph, stream int, source string) bool {
	found := false
	for _, l := range g.Links {
		if l.InNode != stream || l.State == "error" || l.State == "unlinked" {
			continue
		}
		n := g.Nodes[l.OutNode]
		if n == nil || n.Name != source {
			return false
		}
		found = found || l.State == "active"
	}
	return found
}

func waitSharingGraph(ctx context.Context, io *sharingIO, check func(*inspect.Graph) (bool, error)) error {
	timer := time.NewTicker(40 * time.Millisecond)
	defer timer.Stop()
	for {
		g, err := io.graph(ctx)
		if err != nil {
			return err
		}
		ok, err := check(g)
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *RealPipeWire) StartOutputSharing(ctx context.Context, sink string) (err error) {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	return r.startOutputSharing(ctx, sink)
}

func (r *RealPipeWire) startOutputSharing(ctx context.Context, sink string) (err error) {
	if r.sharingIO == nil {
		if err := r.checkTestMode(); err != nil {
			return err
		}
	}
	if r.sharing != nil {
		return errors.New("compartilhamento ou restauração já pendente; desative o compartilhamento primeiro")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	io := r.shareBackend()
	g, err := io.graph(ctx)
	if err != nil {
		return err
	}
	session, err := sharingCandidate(g, sink)
	if err != nil {
		return err
	}
	process, err := io.start(sink)
	if err != nil {
		return err
	}
	session.process = process
	session.sink = sink
	r.sharing = session
	defer func() {
		if err != nil {
			// Cancellation (including panic) cannot cancel rollback. The transport
			// is killed first, even when the original route cannot be restored.
			cleanup, stop := context.WithTimeout(context.Background(), r.timeout)
			defer stop()
			err = errors.Join(err, r.stopOutputSharing(cleanup))
		}
	}()
	err = waitSharingGraph(ctx, io, func(g *inspect.Graph) (bool, error) {
		if !sameSharingNode(g, session.stream) {
			return false, errors.New("a captura da chamada foi alterada antes de iniciar o compartilhamento")
		}
		for _, n := range g.Nodes {
			if n.Name == sharingSource && n.Serial > 0 {
				session.sharedSerial = n.Serial
				// A passive monitor may activate only after the call is connected.
				// Source discovery precedes redirection; transport is checked after it.
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	session.changed = true // A failing command can still have changed metadata.
	if err = io.target(ctx, session.stream.ID, &inspect.TargetMetadata{Value: strconv.Itoa(session.sharedSerial), Type: "Spa:Id"}); err != nil {
		return err
	}
	return waitSharingGraph(ctx, io, func(g *inspect.Graph) (bool, error) {
		if !sameSharingNode(g, session.stream) {
			return false, errors.New("a chamada desapareceu durante o compartilhamento")
		}
		return exclusiveCapture(g, session.stream.ID, sharingSource) && sharingFeed(g, sink), nil
	})
}

func (r *RealPipeWire) StopOutputSharing(ctx context.Context) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	return r.stopOutputSharing(ctx)
}

func (r *RealPipeWire) stopOutputSharing(ctx context.Context) error {
	if r.sharingIO == nil {
		if err := r.checkTestMode(); err != nil {
			return err
		}
	}
	s := r.sharing
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	// Stop sending output before attempting any restoration.
	if err := s.process.stop(ctx); err != nil {
		return err
	}
	io := r.shareBackend()
	if !s.changed {
		r.sharing = nil
		return nil
	}
	g, err := io.graph(ctx)
	if err != nil {
		return err
	}
	if !sameSharingNode(g, s.stream) {
		r.sharing = nil
		return nil
	}
	target, exists := g.CaptureTargets[s.stream.ID]
	owned := exists && target.Value == strconv.Itoa(s.sharedSerial)
	restoring := s.restoring && ((!exists && s.previous == nil) || (exists && s.previous != nil && target == *s.previous))
	if !owned && !restoring {
		// Someone changed the stream manually. Do not overwrite that decision.
		r.sharing = nil
		return nil
	}
	if !sameSharingNode(g, s.original) {
		return errors.New("compartilhamento encerrado; entrada original indisponível, restauração pendente")
	}
	// Restore the previous policy exactly, including absence of target.object.
	s.restoring = true
	if err := io.target(ctx, s.stream.ID, s.previous); err != nil {
		return err
	}
	err = waitSharingGraph(ctx, io, func(g *inspect.Graph) (bool, error) {
		if !sameSharingNode(g, s.stream) {
			return true, nil
		}
		if !sameSharingNode(g, s.original) {
			return false, errors.New("entrada original desapareceu durante a restauração")
		}
		return exclusiveCapture(g, s.stream.ID, s.original.Name), nil
	})
	if err == nil {
		r.sharing = nil
	}
	return err
}

func (r *RealPipeWire) ReadOutputSharing(ctx context.Context) (adapters.OutputSharingState, error) {
	if err := r.checkTestMode(); err != nil {
		return adapters.OutputSharingState{}, err
	}
	return r.readOutputSharing(ctx)
}

func (r *RealPipeWire) readOutputSharing(ctx context.Context) (adapters.OutputSharingState, error) {
	if r.sharingIO == nil {
		if err := r.checkTestMode(); err != nil {
			return adapters.OutputSharingState{}, err
		}
	}
	state, err := r.sharingTransportState()
	if err != nil || r.sharing == nil {
		return state, err
	}
	g, err := r.shareBackend().graph(ctx)
	if err != nil {
		return state, err
	}
	return r.sharingStateFromGraph(g)
}

func (r *RealPipeWire) sharingTransportState() (adapters.OutputSharingState, error) {
	s := r.sharing
	if s == nil {
		return adapters.OutputSharingState{}, nil
	}
	select {
	case <-s.process.done:
		return adapters.OutputSharingState{RestorePending: true}, errors.New("transporte de compartilhamento encerrado; desative o compartilhamento para concluir a restauração")
	default:
		// Unknown routing must never hide a live transport.
		return adapters.OutputSharingState{RestorePending: true}, nil
	}
}

func (r *RealPipeWire) sharingStateFromGraph(g *inspect.Graph) (adapters.OutputSharingState, error) {
	state, err := r.sharingTransportState()
	if err != nil || r.sharing == nil {
		return state, err
	}
	s := r.sharing
	if !sameSharingNode(g, s.stream) || !exclusiveCapture(g, s.stream.ID, sharingSource) || !sharingFeed(g, s.sink) {
		return state, errors.New("rota de captura do compartilhamento não mais confirmada")
	}
	return adapters.OutputSharingState{Active: true}, nil
}

func sharingFeed(g *inspect.Graph, sink string) bool {
	for _, link := range g.Links {
		source, capture := g.Nodes[link.OutNode], g.Nodes[link.InNode]
		if link.State == "active" && source != nil && capture != nil && source.Name == sink && capture.Name == "easyprofile_share_capture" {
			return true
		}
	}
	return false
}
