package pipewire

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/toru/easy-profile-app/internal/inspect"
)

func defaultFixture(kind, effective, configured string) *inspect.Graph {
	class := "Audio/Sink"
	if kind == "source" {
		class = "Audio/Source/Virtual"
	}
	return &inspect.Graph{
		Nodes: map[int]*inspect.NodeInfo{42: {ID: 42, Name: "processed", MediaClass: class}},
		Defaults: map[string]string{
			"default.audio." + kind: effective, "default.configured.audio." + kind: configured,
		},
	}
}

func TestEnsureDefaultConfirmsBothValuesOnFreshSnapshots(t *testing.T) {
	for _, kind := range []string{"sink", "source"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			reads, writes := 0, 0
			err := ensureDefault(ctx, "processed", kind, func(context.Context) (*inspect.Graph, error) {
				reads++
				switch reads {
				case 1:
					return defaultFixture(kind, "physical", "physical"), nil
				case 2:
					return defaultFixture(kind, "physical", "processed"), nil
				default:
					return defaultFixture(kind, "processed", "processed"), nil
				}
			}, func(_ context.Context, node *inspect.NodeInfo) error {
				writes++
				if node.ID != 42 || node.Name != "processed" {
					t.Fatal("selection did not use the fresh endpoint")
				}
				return nil
			})
			if err != nil || reads != 3 || writes != 1 {
				t.Fatalf("unconfirmed or repeated selection: reads=%d writes=%d err=%v", reads, writes, err)
			}
		})
	}
}

func TestEnsureDefaultHealthyDoesNotWriteOrPoll(t *testing.T) {
	for _, kind := range []string{"sink", "source"} {
		reads := 0
		err := ensureDefault(context.Background(), "processed", kind, func(context.Context) (*inspect.Graph, error) {
			reads++
			return defaultFixture(kind, "processed", "processed"), nil
		}, func(context.Context, *inspect.NodeInfo) error {
			t.Fatal("healthy defaults must not be rewritten")
			return nil
		})
		if err != nil || reads != 1 {
			t.Fatalf("healthy readback: reads=%d err=%v", reads, err)
		}
	}
}

func TestEnsureDefaultMismatchIsBoundedAndCancellationStopsReads(t *testing.T) {
	for _, kind := range []string{"sink", "source"} {
		for _, values := range [][2]string{{"physical", "processed"}, {"processed", "physical"}, {"", ""}} {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			reads, writes := 0, 0
			err := ensureDefault(ctx, "processed", kind, func(context.Context) (*inspect.Graph, error) {
				reads++
				return defaultFixture(kind, values[0], values[1]), nil
			}, func(context.Context, *inspect.NodeInfo) error { writes++; return nil })
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) || writes != 1 || reads < 2 {
				t.Fatalf("false confirmation: %s %v reads=%d writes=%d err=%v", kind, values, reads, writes, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ensureDefault(ctx, "processed", "source", func(context.Context) (*inspect.Graph, error) {
		t.Fatal("canceled operation read graph")
		return nil, nil
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestEnsureDefaultRejectsMissingAmbiguousOrWrongClassEndpoints(t *testing.T) {
	for _, kind := range []string{"sink", "source"} {
		for _, fault := range []string{"missing", "ambiguous", "wrong class", "removed after selection"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				reads, writes := 0, 0
				err := ensureDefault(context.Background(), "processed", kind, func(context.Context) (*inspect.Graph, error) {
					reads++
					g := defaultFixture(kind, "processed", "processed")
					switch fault {
					case "missing":
						delete(g.Nodes, 42)
					case "ambiguous":
						n := *g.Nodes[42]
						n.ID = 43
						g.Nodes[43] = &n
					case "wrong class":
						g.Nodes[42].MediaClass = "Stream/Input/Audio"
					case "removed after selection":
						g.Defaults["default.audio."+kind] = "physical"
						if reads > 1 {
							delete(g.Nodes, 42)
						}
					}
					return g, nil
				}, func(context.Context, *inspect.NodeInfo) error { writes++; return nil })
				if err == nil || (fault != "removed after selection" && writes != 0) || (fault == "removed after selection" && writes != 1) {
					t.Fatalf("invalid endpoint accepted: writes=%d err=%v", writes, err)
				}
			})
		}
	}
}

func TestEnsureDefaultPropagatesSelectionAndReadFailures(t *testing.T) {
	want := errors.New("backend failed")
	for _, fault := range []string{"initial read", "selection", "readback"} {
		reads, writes := 0, 0
		err := ensureDefault(context.Background(), "processed", "source", func(context.Context) (*inspect.Graph, error) {
			reads++
			if fault == "initial read" || (fault == "readback" && reads > 1) {
				return nil, want
			}
			return defaultFixture("source", "physical", "physical"), nil
		}, func(context.Context, *inspect.NodeInfo) error {
			writes++
			if fault == "selection" {
				return want
			}
			return nil
		})
		if !errors.Is(err, want) || (fault == "initial read" && writes != 0) {
			t.Fatalf("%s: %v", fault, err)
		}
	}
}
