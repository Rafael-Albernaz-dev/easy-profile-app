#!/usr/bin/env python3
"""Passive Linux cgroup-v2 audio-service sampler; never calls audio commands.

The operator supplies the scenario/workload. A capture alone proves neither
feature activation nor an idle machine; use performance-impact-gate.py on an
accompanied, comparable off/on pair with separate safety/transport evidence.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import resource
import time
import uuid


NAMES = {
    "easyprofiled": "easyprofilectl", "easyeffects": "easyeffects",
    "pipewire": "pipewire", "pipewire-pulse": "pipewire-pulse",
    "wireplumber": "wireplumber", "gnome-shell": "gnome-shell",
}


def key_values(path):
    return {line.split()[0]: int(line.split()[1])
            for line in path.read_text().splitlines() if len(line.split()) == 2}


def processes():
    found = {label: [] for label in NAMES}
    for path in Path("/proc").glob("[0-9]*"):
        try:
            if path.stat().st_uid != os.getuid():
                continue
            comm = (path / "comm").read_text().strip()
            for label, name in NAMES.items():
                if comm != name:
                    continue
                cg = (path / "cgroup").read_text().split("0::", 1)[1].splitlines()[0]
                # Several PipeWire processes can exist; only its main service.
                if label == "pipewire" and not cg.endswith("/pipewire.service"):
                    continue
                if label == "easyprofiled" and not cg.endswith("/easyprofiled.service"):
                    continue
                raw = (path / "stat").read_text().rsplit(")", 1)[1].split()
                exe = path / "exe"
                found[label].append({"pid": int(path.name), "start_ticks": int(raw[19]),
                                     "cgroup": cg,
                                     "exe_sha256": hashlib.sha256(exe.read_bytes()).hexdigest()})
        except (OSError, ValueError, IndexError):
            continue
    for label, matches in found.items():
        if len(matches) != 1:
            raise ValueError(f"{label}: expected one owned process, found {len(matches)}")
    result = {label: matches[0] for label, matches in found.items()}
    if len({p["cgroup"] for p in result.values()}) != len(result):
        raise ValueError("shared cgroup would double-count services")
    return result


def sample(group):
    root = Path("/sys/fs/cgroup") / group["cgroup"].lstrip("/")
    cpu = key_values(root / "cpu.stat")
    # Confirm original PID identity; recreated/restarted scopes invalidate a pair.
    raw = Path(f"/proc/{group['pid']}/stat").read_text().rsplit(")", 1)[1].split()
    if int(raw[19]) != group["start_ticks"]:
        raise ValueError("process identity changed during capture")
    actual_cgroup = Path(f"/proc/{group['pid']}/cgroup").read_text().split("0::", 1)[1].splitlines()[0]
    if actual_cgroup != group["cgroup"]:
        raise ValueError("process cgroup changed during capture")
    quotas = []
    for ancestor in (root, *root.parents):
        if ancestor == Path("/sys/fs"):
            break
        quota = ancestor / "cpu.max"
        if quota.exists():
            quotas.append({"cgroup": str(ancestor), "value": quota.read_text().strip()})
    return {"cpu_usec": cpu["usage_usec"], "nr_throttled": cpu.get("nr_throttled", 0),
            "throttled_usec": cpu.get("throttled_usec", 0),
            "memory_bytes": int((root / "memory.current").read_text()),
            "tasks": int((root / "pids.current").read_text()),
            "memory_events": key_values(root / "memory.events"),
            "cpu_max": quotas}


def context():
    cpu = [int(v) for v in Path("/proc/stat").read_text().splitlines()[0].split()[1:]]
    pressure = Path("/proc/pressure/cpu").read_text().splitlines()[0].split()
    return {"cpu_total_ticks": sum(cpu[:8]), "cpu_idle_ticks": cpu[3] + cpu[4],
            "cpu_pressure_usec": int(next(v.split("=", 1)[1] for v in pressure if v.startswith("total="))),
            "loadavg": [float(v) for v in Path("/proc/loadavg").read_text().split()[:3]]}


def percentile(values, fraction):
    import math
    return sorted(values)[max(0, math.ceil(len(values) * fraction) - 1)]


def capture(duration, interval, scenario, workload, notes):
    groups = processes()
    start_wall = datetime.datetime.now(datetime.timezone.utc).isoformat()
    start = time.monotonic()
    observer_start = resource.getrusage(resource.RUSAGE_SELF)
    first_context = context()
    samples = [(start, {label: sample(group) for label, group in groups.items()})]
    while time.monotonic() - start < duration:
        time.sleep(min(interval, max(0, duration - (time.monotonic() - start))))
        samples.append((time.monotonic(), {label: sample(group) for label, group in groups.items()}))
    elapsed = samples[-1][0] - start
    last_context = context()
    if processes() != groups:
        raise ValueError("service process/binary identity changed during capture")
    observer_end = resource.getrusage(resource.RUSAGE_SELF)
    observer_cpu = (observer_end.ru_utime + observer_end.ru_stime
                    - observer_start.ru_utime - observer_start.ru_stime)
    result = {}
    for label, group in groups.items():
        first, last = samples[0][1][label], samples[-1][1][label]
        rates = [(b[1][label]["cpu_usec"] - a[1][label]["cpu_usec"]) / (b[0] - a[0]) / 10_000
                 for a, b in zip(samples, samples[1:])]
        result[label] = {**group,
                         "cpu_mean_percent_one_core": (last["cpu_usec"] - first["cpu_usec"]) / elapsed / 10_000,
                         "cpu_p95_percent_one_core": percentile(rates, .95),
                         "cpu_peak_percent_one_core": max(rates),
                         "memory_peak_bytes": max(s[1][label]["memory_bytes"] for s in samples),
                         "tasks_peak": max(s[1][label]["tasks"] for s in samples),
                         "nr_throttled_delta": last["nr_throttled"] - first["nr_throttled"],
                         "throttled_usec_delta": last["throttled_usec"] - first["throttled_usec"],
                         "memory_events_delta": {key: last["memory_events"][key] - first["memory_events"][key]
                                                 for key in first["memory_events"]},
                         "cpu_max": last["cpu_max"]}
    total = last_context["cpu_total_ticks"] - first_context["cpu_total_ticks"]
    idle = last_context["cpu_idle_ticks"] - first_context["cpu_idle_ticks"]
    return {"format": 1, "scope": "live-passive-cgroup-v2", "capture_id": str(uuid.uuid4()),
            "captured_at": start_wall, "boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
            "scenario": scenario, "workload_id": workload, "notes": notes,
            "duration_seconds": elapsed, "sample_interval_seconds": interval, "sample_count": len(samples),
            "observer_cpu_percent_one_core": observer_cpu / elapsed * 100,
            "system_cpu_busy_percent": (1 - idle / total) * 100,
            "cpu_pressure_percent": (last_context["cpu_pressure_usec"] - first_context["cpu_pressure_usec"]) / elapsed / 10_000,
            "loadavg_start": first_context["loadavg"], "loadavg_end": last_context["loadavg"],
            "services": result,
            "limitations": ["operator-declared scenario; no audio commands or feature activation",
                            "cgroup CPU includes children, not a subprocess creation counter",
                            "sampled memory/tasks/CPU peaks may miss shorter peaks",
                            "no XRUN, audible stutter, call transport or input mute measurement",
                            "GNOME cgroup includes the whole Shell; extension cost cannot be isolated"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--scenario", choices=("baseline", "boost", "sharing", "push-to-talk"), required=True)
    parser.add_argument("--workload-id", required=True, help="same workload identifier for a comparable pair")
    parser.add_argument("--notes", default="")
    parser.add_argument("--duration", type=float, default=60)
    parser.add_argument("--interval", type=float, default=.25)
    args = parser.parse_args()
    if not 60 <= args.duration <= 300 or not .1 <= args.interval <= 1:
        parser.error("duration must be 60..300 seconds; interval .1..1 second")
    if os.environ.get("EASYPROFILE_TEST_MODE"):
        parser.error("live sampling is forbidden in automated test mode")
    if args.output.exists():
        parser.error("output already exists; captures are never overwritten")
    data = capture(args.duration, args.interval, args.scenario, args.workload_id, args.notes)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x") as out:
        json.dump(data, out, indent=2)
        out.write("\n")
    print(f"Passive capture: {args.output}; {data['duration_seconds']:.2f}s; no feature certified")


if __name__ == "__main__":
    main()
