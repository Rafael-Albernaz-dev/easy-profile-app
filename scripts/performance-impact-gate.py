#!/usr/bin/env python3
"""Score comparable live service captures; fake results never certify DSP impact."""
import argparse
import hashlib
import json
import math
from pathlib import Path
import sys

from importlib.util import module_from_spec, spec_from_file_location

spec = spec_from_file_location("impact_sampler", Path(__file__).with_name("measure-service-impact.py"))
sampler = module_from_spec(spec)
spec.loader.exec_module(sampler)

FLOOR, TARGET = 75, 80
MIB = 1024 * 1024
# Provisional operational budgets, separate from the existing fake/parser gates.
# name, weight, target (full points), ceiling (zero points), unit
CRITERIA = (
    ("daemon_mean", 25, 1, 5, "% of one core"),
    ("daemon_peak", 10, 25, 75, "% of one core; sampled"),
    ("neighbors_delta", 25, 2, 10, "percentage points of one core"),
    ("daemon_memory", 15, 32, 48, "MiB; cgroup including children"),
    ("daemon_tasks", 5, 32, 96, "threads/processes; sampled"),
    ("pressure_delta", 20, .5, 3, "percentage points of CPU pressure"),
)


def numeric(value, label):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0:
        raise ValueError(f"invalid nonnegative finite metric: {label}")
    return value


def validate_capture(data):
    if data.get("format") != 1 or data.get("scope") != "live-passive-cgroup-v2":
        raise ValueError("requires live cgroup captures; fake/engineering evidence cannot be substituted")
    for key in ("capture_id", "boot_id", "workload_id", "scenario"):
        if not isinstance(data.get(key), str) or not data[key].strip():
            raise ValueError(f"missing capture identity: {key}")
    duration = numeric(data.get("duration_seconds"), "duration")
    interval = numeric(data.get("sample_interval_seconds"), "interval")
    if not 60 <= duration <= 301 or not .1 <= interval <= 1:
        raise ValueError("requires 60..300s captures with .1..1s interval")
    if numeric(data.get("sample_count"), "sample count") < .9 * duration / interval:
        raise ValueError("incomplete sampling window")
    for key in ("cpu_pressure_percent", "system_cpu_busy_percent"):
        numeric(data.get(key), key)
    for label in sampler.NAMES:
        service = data["services"][label]
        for key in ("pid", "start_ticks", "cpu_mean_percent_one_core", "cpu_p95_percent_one_core",
                    "cpu_peak_percent_one_core", "memory_peak_bytes", "tasks_peak", "nr_throttled_delta",
                    "throttled_usec_delta"):
            numeric(service.get(key), f"{label}.{key}")
        for key in ("cgroup", "exe_sha256"):
            if not isinstance(service.get(key), str) or not service[key]:
                raise ValueError(f"missing service identity: {label}.{key}")
        for key in ("high", "max", "oom", "oom_kill"):
            numeric(service["memory_events_delta"].get(key), f"{label}.memory_events.{key}")
        if not isinstance(service.get("cpu_max"), list):
            raise ValueError(f"missing CPU quota ancestry: {label}")
        for quota in service["cpu_max"]:
            if not isinstance(quota.get("value"), str) or len(quota["value"].split()) != 2:
                raise ValueError(f"invalid CPU quota ancestry: {label}")


def score_metric(value, weight, target, ceiling):
    if value <= target:
        return float(weight)
    return max(0, weight * (ceiling - value) / (ceiling - target))


STATUS_LABELS = ("baseline_start", "baseline_end", "candidate_start", "candidate_end")


def audio_status_blockers(records, scenario):
    """Four actual read-only status snapshots, not just an operator workload ID."""
    if not isinstance(records, dict) or set(records) != set(STATUS_LABELS):
        return ["missing four audio status records: baseline/candidate start/end"]
    blockers, fingerprints = [], {}
    mode_key = {"boost": "output_boost", "sharing": "output_sharing", "push-to-talk": "push_to_talk"}[scenario]
    for label in STATUS_LABELS:
        state = records[label]
        if not isinstance(state, dict):
            blockers.append(f"invalid audio status record: {label}")
            continue
        for key, expected in (("last_error", ""), ("observe_only", False), ("panic_active", False),
                              ("dsp_bound", True), ("monitor_volumes_confirmed", True),
                              ("protection_state", "confirmed")):
            if state.get(key) != expected or (isinstance(expected, bool) and state.get(key) is not expected):
                blockers.append(f"{label}: unconfirmed healthy audio state {key}")
        if state.get(mode_key) is not label.startswith("candidate"):
            blockers.append(f"{label}: expected feature mode not confirmed ({mode_key})")
        if scenario == "boost":
            if state.get("output_dsp_idle") is not False:
                blockers.append(f"{label}: output idle/unknown; continuous playback workload changed")
            if state.get("output_boost_confirmed") is not True:
                blockers.append(f"{label}: native output DSP confirmation missing")
        fingerprint = {}
        for key in ("output_volume", "mic_level"):
            value = state.get(key)
            if type(value) is not int or not 0 <= value <= (150 if key == "output_volume" else 100):
                blockers.append(f"{label}: invalid/unknown level {key}")
            fingerprint[key] = value
        for key in ("active_preset", "observed_preset", "observed_destination"):
            value = state.get(key)
            if not isinstance(value, str) or not value:
                blockers.append(f"{label}: unknown preset/destination {key}")
            fingerprint[key] = value
        destination = state.get("active_destination")
        destination = destination if isinstance(destination, dict) else {}
        for key in ("id", "port_name"):
            value = destination.get(key)
            if not isinstance(value, str) or not value:
                blockers.append(f"{label}: unknown active destination {key}")
            fingerprint[f"destination_{key}"] = value
        if state.get("active_preset") != state.get("observed_preset") or destination.get("port_name") != state.get("observed_destination"):
            blockers.append(f"{label}: desired/observed preset or destination mismatch")
        for observed, desired in (("observed_output_mute", "output_muted"),
                                  ("observed_mic_virtual_mute", "mic_virtual_muted"),
                                  ("observed_mic_physical_mute", "mic_physical_muted")):
            value = state.get(observed)
            if type(value) is not bool or state.get(desired) is not value:
                blockers.append(f"{label}: unknown/inconsistent independent mute {observed}")
            fingerprint[observed] = value
        fingerprints[label] = fingerprint
    if len(fingerprints) == 4:
        for key in fingerprints[STATUS_LABELS[0]]:
            if any(fingerprints[label][key] != fingerprints[STATUS_LABELS[0]][key] for label in STATUS_LABELS[1:]):
                blockers.append(f"audio drift across four status records: {key}")
    return blockers


def evaluate(baseline, candidate=None, validation=None):
    result = {"scope": "operational live service impact; no fake/DSP equivalence",
              "floor": FLOOR, "target": TARGET, "score": None, "status": "NOT_CERTIFIED",
              "blockers": [], "criteria": [], "services": []}
    validate_capture(baseline)
    if baseline["scenario"] != "baseline":
        raise ValueError("first capture must be the feature-off baseline")
    for label in sampler.NAMES:
        b = baseline["services"][label]
        result["services"].append({"service": label,
                                   "baseline_cpu": b["cpu_mean_percent_one_core"],
                                   "baseline_memory_mib": b["memory_peak_bytes"] / MIB,
                                   "candidate_cpu": None, "cpu_delta": None})
    if candidate is None:
        result["blockers"].append("missing comparable feature-on capture and accompanied safety/transport evidence")
        return result
    validate_capture(candidate)
    if candidate["scenario"] not in ("boost", "sharing", "push-to-talk"):
        raise ValueError("candidate must identify one feature")
    if candidate["capture_id"] == baseline["capture_id"]:
        raise ValueError("two distinct captures are required")
    for key in ("boot_id", "workload_id", "sample_interval_seconds"):
        if baseline[key] != candidate[key]:
            result["blockers"].append(f"incomparable {key}")
    if abs(baseline["system_cpu_busy_percent"] - candidate["system_cpu_busy_percent"]) > 15:
        result["blockers"].append("host CPU context changed by >15 percentage points; repeat paired workload")
    for row in result["services"]:
        label = row["service"]
        b, c = baseline["services"][label], candidate["services"][label]
        row.update(candidate_cpu=c["cpu_mean_percent_one_core"],
                   cpu_delta=c["cpu_mean_percent_one_core"] - b["cpu_mean_percent_one_core"],
                   candidate_memory_mib=c["memory_peak_bytes"] / MIB)
        for key in ("pid", "start_ticks", "exe_sha256", "cgroup"):
            if b[key] != c[key]:
                result["blockers"].append(f"{label}: changed {key}; repeat baseline with candidate installed")
        for name, sample in (("baseline", b), ("candidate", c)):
            if any(quota["value"].split()[0] != "max" for quota in sample["cpu_max"]):
                result["blockers"].append(f"{label} {name}: finite ancestor CPU quota; ancestor throttling requires separate measurement")
            if sample["nr_throttled_delta"] or sample["throttled_usec_delta"]:
                result["blockers"].append(f"{label} {name}: CPU throttling")
            if any(sample["memory_events_delta"].get(key, 0) for key in ("high", "max", "oom", "oom_kill", "oom_group_kill")):
                result["blockers"].append(f"{label} {name}: memory pressure/limit/OOM event")
    # A real idle baseline must respect the existing <1% one-core budget too.
    # The operator must explicitly declare idle; do not label playback idle.
    if validation and validation.get("baseline_idle") is True and baseline["services"]["easyprofiled"]["cpu_mean_percent_one_core"] >= 1:
        result["blockers"].append("declared idle baseline exceeds existing <1% one-core CPU budget")
    if not validation:
        result["blockers"].append("missing accompanied safety/transport evidence")
    else:
        result["blockers"].extend(audio_status_blockers(validation.get("audio_status_records"), candidate["scenario"]))
        if validation.get("source") != "accompanied-live":
            result["blockers"].append("safety/transport evidence must be accompanied-live")
        for name, value in (("baseline_capture_id", baseline["capture_id"]),
                            ("candidate_capture_id", candidate["capture_id"]),
                            ("workload_id", baseline["workload_id"])):
            if validation.get(name) != value:
                result["blockers"].append(f"safety evidence {name} mismatch")
        for name in ("workload_controlled", "feature_off_baseline_confirmed", "feature_on_candidate_confirmed",
                     "presets_identical", "independent_mutes_preserved", "restoration_verified", "transport_or_dsp_confirmed"):
            if validation.get(name) is not True:
                result["blockers"].append(f"unconfirmed safety/transport: {name}")
        try:
            bx = numeric(validation.get("baseline_xruns"), "baseline XRUNs")
            cx = numeric(validation.get("candidate_xruns"), "candidate XRUNs")
            if bx != 0 or cx != 0:
                result["blockers"].append("XRUNs observed; clean paired windows required")
        except ValueError as error:
            result["blockers"].append(str(error))
        if validation.get("candidate_audible_stutter") is not False:
            result["blockers"].append("audible stutter absent not confirmed")
        if not isinstance(validation.get("evidence_references"), list) or not validation["evidence_references"] or not all(isinstance(x, str) and x.strip() for x in validation["evidence_references"]):
            result["blockers"].append("missing separate evidence references; boolean declarations alone are insufficient")
        for window, counters in validation.get("audio_error_increments", {}).items():
            if not isinstance(counters, dict):
                result["blockers"].append(f"{window}: missing comparable audio ERR counters")
                continue
            for node, delta in counters.items():
                if numeric(delta, f"{window} ERR node {node}") > 0:
                    result["blockers"].append(f"{window}: new pw-top ERR increment at node {node}; ERR includes XRUNs and errors")
    # Never assign provisional points to an incomparable/unsafe pair.
    if result["blockers"]:
        return result
    d = candidate["services"]["easyprofiled"]
    values = {"daemon_mean": d["cpu_mean_percent_one_core"], "daemon_peak": d["cpu_peak_percent_one_core"],
              "neighbors_delta": sum(max(0, row["cpu_delta"]) for row in result["services"] if row["service"] != "easyprofiled"),
              "daemon_memory": d["memory_peak_bytes"] / MIB, "daemon_tasks": d["tasks_peak"],
              "pressure_delta": max(0, candidate["cpu_pressure_percent"] - baseline["cpu_pressure_percent"])}
    for name, weight, target, ceiling, unit in CRITERIA:
        value = values[name]
        result["criteria"].append({"criterion": name, "value": value, "unit": unit, "weight": weight,
                                   "target": target, "ceiling": ceiling,
                                   "points": score_metric(value, weight, target, ceiling)})
        if value >= ceiling:
            result["blockers"].append(f"operational ceiling reached: {name}")
    if result["blockers"]:
        return result
    result["score"] = round(sum(row["points"] for row in result["criteria"]), 2)
    result["status"] = "TARGET_MET" if result["score"] >= TARGET else "FLOOR_MET" if result["score"] >= FLOOR else "FAIL"
    return result


def markdown(result):
    score = "não certificado" if result["score"] is None else f"{result['score']:.2f}%"
    lines = [f"Score operacional: {score}; piso {FLOOR}%; meta {TARGET}%; {result['status']}.", "",
             "| Serviço (CPU inclui filhos do cgroup) | CPU baseline¹ | CPU feature¹ | Δ CPU observado² | Memória baseline → feature³ |",
             "|---|---:|---:|---:|---:|"]
    for row in result["services"]:
        candidate = "pendente" if row["candidate_cpu"] is None else f"{row['candidate_cpu']:.2f}%"
        delta = "pendente" if row["cpu_delta"] is None else f"{row['cpu_delta']:+.2f} pp"
        memory = "pendente" if row.get("candidate_memory_mib") is None else f"{row['candidate_memory_mib']:.2f}"
        lines.append(f"| {row['service']} | {row['baseline_cpu']:.2f}% | {candidate} | {delta} | {row['baseline_memory_mib']:.2f} → {memory} MiB |")
    lines += ["", "¹ Percentual de um núcleo; não é score. ² Diferença observada, sem atribuição causal automática.",
              "³ Pico amostrado do cgroup, não RSS individual. Sem contagem exata de spawns ou XRUNs neste coletor."]
    if result["criteria"]:
        lines += ["", "| Critério | Medido | Meta | Teto | Peso | Pontos |", "|---|---:|---:|---:|---:|---:|"]
        for row in result["criteria"]:
            lines.append(f"| {row['criterion']} ({row['unit']}) | {row['value']:.2f} | {row['target']} | {row['ceiling']} | {row['weight']} | {row['points']:.2f} |")
    if result["blockers"]:
        lines += ["", "Bloqueios (não compensáveis por pontuação):"] + [f"- {item}" for item in result["blockers"]]
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("baseline", type=Path)
    parser.add_argument("--candidate", type=Path)
    parser.add_argument("--validation", type=Path)
    parser.add_argument("--json-output", type=Path)
    args = parser.parse_args()
    try:
        read = lambda path: json.loads(path.read_text()) if path else None
        validation = read(args.validation)
        status_evidence = {}
        if validation:
            # CLI requires file-backed snapshots; embedded dictionaries are only
            # used by the in-memory regression tests of evaluate().
            validation.pop("audio_status_records", None)
            files = validation.get("audio_status_files")
            if isinstance(files, dict) and set(files) == set(STATUS_LABELS):
                records = {}
                for label, filename in files.items():
                    path = Path(filename)
                    if not path.is_absolute():
                        path = args.validation.parent / path
                    data = path.read_bytes()
                    records[label] = json.loads(data)
                    status_evidence[label] = {"file": str(path.resolve()), "sha256": hashlib.sha256(data).hexdigest()}
                validation["audio_status_records"] = records
        result = evaluate(read(args.baseline), read(args.candidate), validation)
        if status_evidence:
            result["audio_status_evidence"] = status_evidence
        if args.json_output:
            args.json_output.write_text(json.dumps(result, indent=2) + "\n")
        print(markdown(result), end="")
    except (OSError, ValueError, KeyError, TypeError) as error:
        parser.error(str(error))
    return 0 if result["status"] in ("TARGET_MET", "FLOOR_MET") else 1


if __name__ == "__main__":
    sys.exit(main())
