package pipewire

import (
	"context"
	"errors"

	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/inspect"
)

func observationFromGraph(g *inspect.Graph, sink, source, physicalSink, micPrefix string) (adapters.PipeWireObservation, error) {
	var obs adapters.PipeWireObservation
	var graphErr, rawErr, routeErr error
	obs.Graph, graphErr = graphState(g, sink, source, physicalSink, micPrefix)
	obs.GraphError = graphErr
	obs.RawMicStreams, rawErr = rawMicStreams(g, micPrefix)
	obs.SinkPort, routeErr = sinkPort(g, physicalSink)
	obs.MicMonitoring = micMonitoring(g, source, sink)
	obs.MicMonitoringKnown = true
	return obs, errors.Join(graphErr, rawErr, routeErr)
}

func (r *RealPipeWire) ReadObservation(ctx context.Context, sink, source, physicalSink, micPrefix string) (adapters.PipeWireObservation, error) {
	if err := r.checkTestMode(); err != nil {
		return adapters.PipeWireObservation{}, err
	}
	return r.readObservation(ctx, sink, source, physicalSink, micPrefix, r.graph)
}

func (r *RealPipeWire) readObservation(ctx context.Context, sink, source, physicalSink, micPrefix string, readGraph func(context.Context) (*inspect.Graph, error)) (adapters.PipeWireObservation, error) {
	g, err := readGraph(ctx)
	if err != nil {
		// A failed graph read must not conceal a live sharing transport.
		sharing, sharingErr := r.sharingTransportState()
		return adapters.PipeWireObservation{Sharing: sharing, GraphError: err}, errors.Join(err, sharingErr)
	}
	obs, err := observationFromGraph(g, sink, source, physicalSink, micPrefix)
	var sharingErr error
	obs.Sharing, sharingErr = r.sharingStateFromGraph(g)
	return obs, errors.Join(err, sharingErr)
}
