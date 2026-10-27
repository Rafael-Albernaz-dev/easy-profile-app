#!/usr/bin/env python3
"""In-memory gate regressions; never samples live services or executes audio."""
import copy
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("impact_gate", Path(__file__).with_name("performance-impact-gate.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


def fixtures():
    baseline = {"format": 1, "scope": "live-passive-cgroup-v2", "capture_id": "off", "boot_id": "boot",
                "scenario": "baseline", "workload_id": "fixture-not-real", "duration_seconds": 60,
                "sample_interval_seconds": .25, "sample_count": 241,
                "system_cpu_busy_percent": 20, "cpu_pressure_percent": 1, "services": {}}
    for i, label in enumerate(gate.sampler.NAMES):
        baseline["services"][label] = {"pid": 100 + i, "start_ticks": 10, "cgroup": f"/{label}", "exe_sha256": "fixture",
                                        "cpu_mean_percent_one_core": .1, "cpu_p95_percent_one_core": 2,
                                        "cpu_peak_percent_one_core": 10, "memory_peak_bytes": 10 * gate.MIB,
                                        "tasks_peak": 10, "nr_throttled_delta": 0, "throttled_usec_delta": 0,
                                        "cpu_max": [{"cgroup": "/fixture", "value": "max 100000"}],
                                        "memory_events_delta": {"oom": 0, "oom_kill": 0, "high": 0, "max": 0}}
    candidate = copy.deepcopy(baseline)
    candidate.update(capture_id="on", scenario="boost")
    validation = {"source": "accompanied-live", "baseline_capture_id": "off", "candidate_capture_id": "on",
                  "workload_id": "fixture-not-real", "baseline_idle": False, "workload_controlled": True,
                  "feature_off_baseline_confirmed": True, "feature_on_candidate_confirmed": True,
                  "presets_identical": True, "independent_mutes_preserved": True, "restoration_verified": True,
                  "transport_or_dsp_confirmed": True, "baseline_xruns": 0, "candidate_xruns": 0,
                  "candidate_audible_stutter": False, "evidence_references": ["in-memory-test-fixture"]}
    state = {"output_volume": 45, "mic_level": 100, "active_preset": "fixture-preset", "observed_preset": "fixture-preset",
             "observed_destination": "fixture-port", "active_destination": {"id": "akg", "port_name": "fixture-port"},
             "observed_output_mute": False, "output_muted": False,
             "observed_mic_virtual_mute": True, "mic_virtual_muted": True,
             "observed_mic_physical_mute": False, "mic_physical_muted": False,
             "last_error": "", "observe_only": False, "panic_active": False,
             "dsp_bound": True, "monitor_volumes_confirmed": True, "protection_state": "confirmed",
             "output_dsp_idle": False, "output_boost_confirmed": True}
    validation["audio_status_records"] = {}
    for label in gate.STATUS_LABELS:
        validation["audio_status_records"][label] = {**copy.deepcopy(state), "output_boost": label.startswith("candidate")}
    return baseline, candidate, validation


class OperationalGateTests(unittest.TestCase):
    def test_baseline_alone_never_certifies(self):
        b, _, _ = fixtures()
        result = gate.evaluate(b)
        self.assertIsNone(result["score"])
        self.assertEqual("NOT_CERTIFIED", result["status"])
        self.assertIn("pendente", gate.markdown(result))

    def test_target_and_floor_are_distinct(self):
        b, c, v = fixtures()
        self.assertEqual(100, gate.evaluate(b, c, v)["score"])
        c["services"]["easyprofiled"]["cpu_mean_percent_one_core"] = 3
        c["services"]["easyeffects"]["cpu_mean_percent_one_core"] = 6.1
        result = gate.evaluate(b, c, v)
        self.assertEqual(75, result["score"])
        self.assertEqual("FLOOR_MET", result["status"])
        c["services"]["easyeffects"]["cpu_mean_percent_one_core"] = 4.5
        result = gate.evaluate(b, c, v)
        self.assertEqual(80, result["score"])
        self.assertEqual("TARGET_MET", result["status"])
        c["services"]["easyeffects"]["cpu_mean_percent_one_core"] = 6.2
        self.assertEqual("FAIL", gate.evaluate(b, c, v)["status"])

    def test_safety_cannot_be_compensated_by_perfect_metrics(self):
        for field, bad in (("presets_identical", False), ("independent_mutes_preserved", False),
                           ("restoration_verified", False), ("candidate_xruns", 1),
                           ("candidate_audible_stutter", True), ("evidence_references", []),
                           ("transport_or_dsp_confirmed", False)):
            with self.subTest(field=field):
                b, c, v = fixtures()
                v[field] = bad
                self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_missing_or_fake_safety_cannot_certify(self):
        b, c, v = fixtures()
        self.assertIsNone(gate.evaluate(b, c)["score"])
        v["source"] = "fake"
        self.assertIsNone(gate.evaluate(b, c, v)["score"])
        del v["candidate_xruns"]
        self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_restart_changed_binary_or_uncontrolled_context_invalidates_pair(self):
        for key, value in (("pid", 999), ("start_ticks", 20), ("exe_sha256", "other"), ("cgroup", "/other")):
            with self.subTest(key=key):
                b, c, v = fixtures()
                c["services"]["easyprofiled"][key] = value
                self.assertIsNone(gate.evaluate(b, c, v)["score"])
        b, c, v = fixtures()
        c["system_cpu_busy_percent"] = 50
        self.assertIsNone(gate.evaluate(b, c, v)["score"])
        b, c, v = fixtures()
        v["workload_controlled"] = False
        self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_oom_and_throttling_block_all_services(self):
        for label in gate.sampler.NAMES:
            b, c, v = fixtures()
            c["services"][label]["nr_throttled_delta"] = 1
            self.assertIsNone(gate.evaluate(b, c, v)["score"])
            c["services"][label]["nr_throttled_delta"] = 0
            c["services"][label]["memory_events_delta"]["oom_kill"] = 1
            self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_no_fake_or_short_measurement(self):
        b, c, v = fixtures()
        b["scope"] = "fake"
        with self.assertRaises(ValueError):
            gate.evaluate(b, c, v)
        b, c, v = fixtures()
        b["duration_seconds"] = 10
        with self.assertRaises(ValueError):
            gate.evaluate(b, c, v)

    def test_no_nan_negative_or_boolean_metrics(self):
        for value in (float("nan"), float("inf"), -1, True, None):
            b, c, v = fixtures()
            c["services"]["easyeffects"]["cpu_mean_percent_one_core"] = value
            with self.assertRaises(ValueError):
                gate.evaluate(b, c, v)

    def test_missing_xrun_is_unknown_and_not_zero(self):
        b, c, v = fixtures()
        del v["candidate_xruns"]
        self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_missing_kernel_counters_do_not_become_zero(self):
        b, c, v = fixtures()
        c["services"]["pipewire"]["memory_events_delta"] = {}
        with self.assertRaises(ValueError):
            gate.evaluate(b, c, v)
        b, c, v = fixtures()
        del c["services"]["easyprofiled"]["cpu_max"]
        with self.assertRaises(ValueError):
            gate.evaluate(b, c, v)

    def test_finite_ancestor_quota_needs_separate_throttling_evidence(self):
        b, c, v = fixtures()
        c["services"]["easyprofiled"]["cpu_max"] = [{"value": "2000 100000"}]
        self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_negative_neighbor_delta_does_not_hide_other_regression(self):
        b, c, v = fixtures()
        c["services"]["easyeffects"]["cpu_mean_percent_one_core"] += 4
        b["services"]["gnome-shell"]["cpu_mean_percent_one_core"] = 10
        result = gate.evaluate(b, c, v)
        metric = next(x for x in result["criteria"] if x["criterion"] == "neighbors_delta")
        self.assertAlmostEqual(4, metric["value"])

    def test_ceiling_is_hard_and_score_weights_total_one_hundred(self):
        self.assertEqual(100, sum(x[1] for x in gate.CRITERIA))
        b, c, v = fixtures()
        c["services"]["easyprofiled"]["memory_peak_bytes"] = 48 * gate.MIB
        self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_declared_idle_enforces_existing_budget(self):
        b, c, v = fixtures()
        v["baseline_idle"] = True
        b["services"]["easyprofiled"]["cpu_mean_percent_one_core"] = 1
        self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_four_file_backed_status_snapshots_are_required(self):
        b, c, v = fixtures()
        del v["audio_status_records"]
        self.assertIsNone(gate.evaluate(b, c, v)["score"])
        b, c, v = fixtures()
        del v["audio_status_records"]["baseline_end"]
        self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_actual_volume_drift_blocks_even_when_operator_says_controlled(self):
        b, c, v = fixtures()
        v["audio_status_records"]["baseline_end"]["output_volume"] = 60
        result = gate.evaluate(b, c, v)
        self.assertIsNone(result["score"])
        self.assertEqual([], result["criteria"])
        self.assertTrue(any("output_volume" in item for item in result["blockers"]))

    def test_preset_destination_and_independent_mute_drift_block(self):
        for key, value in (("active_preset", "other"), ("observed_destination", "other"),
                           ("observed_output_mute", True), ("observed_mic_virtual_mute", False),
                           ("observed_mic_physical_mute", True)):
            b, c, v = fixtures()
            v["audio_status_records"]["candidate_end"][key] = value
            self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_idle_and_unknown_dsp_do_not_certify_continuous_boost(self):
        for key, value in (("output_dsp_idle", True), ("output_boost_confirmed", False), ("observed_mic_physical_mute", None)):
            b, c, v = fixtures()
            v["audio_status_records"]["baseline_end"][key] = value
            self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_wrong_mode_and_unknown_status_do_not_pass(self):
        for label in gate.STATUS_LABELS:
            b, c, v = fixtures()
            v["audio_status_records"][label]["output_boost"] = not label.startswith("candidate")
            self.assertIsNone(gate.evaluate(b, c, v)["score"])
        b, c, v = fixtures()
        v["audio_status_records"]["candidate_end"]["output_volume"] = None
        self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_err_increments_block_without_claiming_unique_xrun_count(self):
        b, c, v = fixtures()
        v["audio_error_increments"] = {"baseline": {"40": 0}, "candidate": {"40": 1, "83": 1}}
        result = gate.evaluate(b, c, v)
        self.assertIsNone(result["score"])
        self.assertTrue(any("ERR increment" in item for item in result["blockers"]))

    def test_reset_counter_is_unknown_not_a_negative_error_improvement(self):
        b, c, v = fixtures()
        v["audio_error_increments"] = {"baseline": None}
        self.assertIsNone(gate.evaluate(b, c, v)["score"])

    def test_cli_reads_real_status_files_and_hashes_them(self):
        b, c, v = fixtures()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            v["audio_status_files"] = {}
            for label, state in v["audio_status_records"].items():
                filename = label + '.json'
                (root / filename).write_text(json.dumps(state))
                v["audio_status_files"][label] = filename
            for name, data in (("off.json", b), ("on.json", c), ("validation.json", v)):
                (root / name).write_text(json.dumps(data))
            args = ["gate", str(root / 'off.json'), '--candidate', str(root / 'on.json'),
                    '--validation', str(root / 'validation.json'), '--json-output', str(root / 'result.json')]
            with patch('sys.argv', args), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(0, gate.main())
            result = json.loads((root / 'result.json').read_text())
            self.assertEqual(set(gate.STATUS_LABELS), set(result['audio_status_evidence']))
            self.assertTrue(all(len(x['sha256']) == 64 for x in result['audio_status_evidence'].values()))
            changed = v['audio_status_records']['baseline_end'].copy()
            changed['output_volume'] = 60
            (root / 'baseline_end.json').write_text(json.dumps(changed))
            # Original embedded dictionaries cannot hide drift in a real file.
            with patch('sys.argv', args), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(1, gate.main())

    def test_cli_rejects_embedded_status_without_files(self):
        b, c, v = fixtures()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name, data in (("off.json", b), ("on.json", c), ("validation.json", v)):
                (root / name).write_text(json.dumps(data))
            args = ["gate", str(root / 'off.json'), '--candidate', str(root / 'on.json'),
                    '--validation', str(root / 'validation.json')]
            with patch('sys.argv', args), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(1, gate.main())


if __name__ == "__main__":
    unittest.main()
