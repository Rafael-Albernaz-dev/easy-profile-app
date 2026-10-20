// This explicit, opt-in diagnostic measures ONLY read operations on the live
// audio services. It never constructs or runs a controlling daemon.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"syscall"
	"time"

	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/adapters/easyeffects"
	"github.com/toru/easy-profile-app/internal/adapters/pipewire"
)

const (
	sink     = "easyeffects_sink"
	source   = "easyeffects_source"
	physical = "alsa_output.pci-0000_00_1f.3.analog-stereo"
	mic      = "alsa_input.usb-MU900_KA-1333"
)

type observed struct {
	Graph         adapters.GraphState
	Devices       adapters.DSPDevices
	Raw           []string
	Route, Preset string
	Monitoring    bool
	Mutes         [3]bool
	Sharing       adapters.OutputSharingState
}

func readPass(ctx context.Context, pw *pipewire.RealPipeWire, ee *easyeffects.RealEasyEffects, bulk bool) (observed, error) {
	var s observed
	var errs []error
	if bulk {
		obs, err := pw.ReadObservation(ctx, sink, source, physical, mic)
		s.Graph, s.Raw, s.Route, s.Monitoring, s.Sharing = obs.Graph, obs.RawMicStreams, obs.SinkPort, obs.MicMonitoring, obs.Sharing
		errs = append(errs, err)
	} else {
		var err error
		s.Sharing, err = pw.ReadOutputSharing(ctx)
		errs = append(errs, err)
		s.Graph, err = pw.ReadGraphState(ctx, sink, source, physical, mic)
		errs = append(errs, err)
		s.Raw, err = pw.GetRawMicStreams(ctx, mic)
		errs = append(errs, err)
		s.Route, err = pw.GetSinkPort(ctx, physical)
		errs = append(errs, err)
		s.Monitoring, err = pw.GetMicMonitoring(ctx, source, sink)
		errs = append(errs, err)
	}
	var err error
	s.Devices, err = ee.ReadDevices(ctx)
	errs = append(errs, err)
	_, err = ee.ListAvailablePresets(ctx)
	errs = append(errs, err)
	s.Preset, err = ee.GetActivePreset(ctx)
	errs = append(errs, err)
	for i, node := range []string{sink, source, mic} {
		s.Mutes[i], err = pw.GetNodeMute(ctx, node)
		errs = append(errs, err)
	}
	return s, errors.Join(errs...)
}

type sample struct {
	WallMS float64 `json:"wall_ms"`
	CPUMS  float64 `json:"cpu_ms_self_and_reaped_children"`
}

func cpuSeconds() float64 {
	var self, children syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &self)
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &children)
	return float64(self.Utime.Sec+self.Stime.Sec+children.Utime.Sec+children.Stime.Sec) + float64(self.Utime.Usec+self.Stime.Usec+children.Utime.Usec+children.Stime.Usec)/1e6
}

func measure(pw *pipewire.RealPipeWire, ee *easyeffects.RealEasyEffects, legacy bool) (sample, observed, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start, cpu := time.Now(), cpuSeconds()
	if legacy {
		if _, err := readPass(ctx, pw, ee, false); err != nil {
			return sample{}, observed{}, err
		}
		if _, err := ee.GetActivePreset(ctx); err != nil {
			return sample{}, observed{}, err
		}
	}
	s, err := readPass(ctx, pw, ee, !legacy)
	return sample{WallMS: float64(time.Since(start)) / float64(time.Millisecond), CPUMS: (cpuSeconds() - cpu) * 1000}, s, err
}

func median(samples []sample, field func(sample) float64) float64 {
	values := make([]float64, len(samples))
	for i, s := range samples {
		values[i] = field(s)
	}
	sort.Float64s(values)
	if len(values)%2 == 0 {
		return (values[len(values)/2-1] + values[len(values)/2]) / 2
	}
	return values[len(values)/2]
}

func run() error {
	allowed := flag.Bool("live-read-only", false, "explicitly allow read-only queries to the current PipeWire/EasyEffects services")
	count := flag.Int("samples", 5, "paired samples (1..10)")
	flag.Parse()
	if !*allowed || *count < 1 || *count > 10 || flag.NArg() != 0 {
		return errors.New("requires --live-read-only and --samples between 1 and 10")
	}
	if _, present := os.LookupEnv("EASYPROFILE_TEST_MODE"); present {
		return errors.New("live diagnostic is forbidden in automated test mode")
	}
	pw, ee := pipewire.NewRealPipeWire(3*time.Second), easyeffects.NewRealEasyEffects("", 3*time.Second)
	// Warm identity resolution equally; it remains fresh on every later snapshot.
	if _, _, err := measure(pw, ee, false); err != nil {
		return err
	}
	var before, after []sample
	for range *count {
		old, oldState, err := measure(pw, ee, true)
		if err != nil {
			return err
		}
		updated, newState, err := measure(pw, ee, false)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(oldState, newState) {
			return errors.New("live state changed between paired samples; retry while state is stable")
		}
		before, after = append(before, old), append(after, updated)
	}
	wall := func(s sample) float64 { return s.WallMS }
	cpu := func(s sample) float64 { return s.CPUMS }
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"scope":   "live read-only comparison of equivalent healthy reconciliation reads; not daemon CPU, no writes, no restart, no real commands in automated tests",
		"samples": *count, "legacy": before, "optimized": after,
		"legacy_median_wall_ms": median(before, wall), "optimized_median_wall_ms": median(after, wall),
		"legacy_median_cpu_ms": median(before, cpu), "optimized_median_cpu_ms": median(after, cpu),
		"steady_read_subprocesses_from_code_legacy": 31, "steady_read_subprocesses_from_code_optimized": 11,
		"observations_equal": true,
	})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
