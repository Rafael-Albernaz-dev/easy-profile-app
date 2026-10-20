package pipewire

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestDefaultMetadataAloneWakesReconciliation(t *testing.T) {
	for _, key := range []string{"default.audio.source", "default.configured.audio.source", "default.audio.sink", "default.configured.audio.sink"} {
		for _, value := range []string{`{"name":"physical"}`, `null`} {
			t.Run(key+"/"+value, func(t *testing.T) {
				r := NewRealPipeWire(0)
				events := make(chan struct{}, 1)
				payload := fmt.Sprintf(`[{"id":7,"metadata":[{"subject":0,"key":%q,"value":%s}]}]`, key, value)
				if err := r.watchGraphEvents(strings.NewReader(payload), events); !errors.Is(err, io.EOF) || len(events) != 1 {
					t.Fatalf("metadata-only change ignored: %v events=%d", err, len(events))
				}
			})
		}
	}
}

func TestWatchMetadataCreationRemovalAndBurstCoalescing(t *testing.T) {
	r := NewRealPipeWire(0)
	events := make(chan struct{}, 8)
	payload := `[{"id":7,"type":"PipeWire:Interface:Metadata","props":{"metadata.name":"default"}}]
[{"id":7,"info":null}]
[{"id":8,"type":"PipeWire:Interface:Metadata","props":{"metadata.name":"default"}}]`
	if err := r.watchGraphEvents(strings.NewReader(payload), events); !errors.Is(err, io.EOF) || len(events) != 3 {
		t.Fatalf("metadata lifecycle ignored: %v events=%d", err, len(events))
	}
	events = make(chan struct{}, 1)
	if err := r.watchGraphEvents(strings.NewReader(payload), events); !errors.Is(err, io.EOF) || len(events) != 1 {
		t.Fatal("burst did not coalesce", err)
	}
}

func TestWatchRetainsNodeInvalidationAndRejectsMalformedEvents(t *testing.T) {
	r := NewRealPipeWire(0)
	r.nodeIDCache["processed"] = 42
	r.cacheSerials["processed"] = 100
	events := make(chan struct{}, 8)
	payload := `[{"id":42,"type":"PipeWire:Interface:Node","info":{}}]
[{"id":42,"info":null}]
[{"id":43,"info":null}]
[{"id":44,"type":"PipeWire:Interface:Link","info":{}}]
[{"id":45,"type":"PipeWire:Interface:Device","info":{}}]`
	if err := r.watchGraphEvents(strings.NewReader(payload), events); !errors.Is(err, io.EOF) || len(events) != 5 || len(r.nodeIDCache) != 0 {
		t.Fatalf("lost graph event/cache invalidation: %v events=%d cache=%v", err, len(events), r.nodeIDCache)
	}
	if err := r.watchGraphEvents(strings.NewReader(`[{"id":`), events); err == nil || errors.Is(err, io.EOF) {
		t.Fatal("malformed events accepted", err)
	}
}

func TestUnrelatedMetadataDoesNotScheduleRepair(t *testing.T) {
	r := NewRealPipeWire(0)
	events := make(chan struct{}, 1)
	payload := `[{"id":7,"type":"PipeWire:Interface:Metadata","props":{"metadata.name":"settings"},"metadata":[{"subject":0,"key":"clock.rate","value":48000}]}]
[{"id":8,"metadata":[{"subject":22,"key":"default.audio.source","value":null}]}]`
	if err := r.watchGraphEvents(strings.NewReader(payload), events); !errors.Is(err, io.EOF) || len(events) != 0 {
		t.Fatal("unrelated metadata scheduled repair", err)
	}
}
