#!/usr/bin/env python3
"""Measure only an isolated fake daemon; these numbers do not certify real audio adapters."""
import json, os, pathlib, select, statistics, subprocess, sys, time

if os.environ.get('EASYPROFILE_TEST_MODE') != '1' or os.environ.get('EASYPROFILE_PRIVATE_BUS') != '1':
    raise SystemExit('requires test mode and a private dbus-run-session')
if not os.environ.get('EASYPROFILE_PARENT_BUS_ADDRESS') or os.environ.get('DBUS_SESSION_BUS_ADDRESS') == os.environ['EASYPROFILE_PARENT_BUS_ADDRESS']:
    raise SystemExit('private bus address must differ from parent')

os.environ['GIO_USE_VFS'] = 'local'
binary = pathlib.Path(sys.argv[1]).resolve()
output = pathlib.Path(sys.argv[2]).resolve()
with subprocess.Popen([str(binary), 'daemon', '--fake'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as daemon:
    try:
        if not select.select([daemon.stdout], [], [], 5)[0] or daemon.stdout.readline().strip() != 'easyprofile daemon ready on org.easyprofile.Daemon':
            raise RuntimeError('fake daemon did not become ready')
        def stats():
            raw = pathlib.Path(f'/proc/{daemon.pid}/stat').read_text().rsplit(')', 1)[1].split()
            cpu_ticks = int(raw[11]) + int(raw[12])
            status = pathlib.Path(f'/proc/{daemon.pid}/status').read_text()
            rss = int(next(line.split()[1] for line in status.splitlines() if line.startswith('VmRSS:')))
            return cpu_ticks, rss
        first_ticks, first_rss = stats()
        start = time.monotonic()
        time.sleep(60)
        last_ticks, last_rss = stats()
        elapsed = time.monotonic() - start
        if daemon.poll() is not None:
            raise RuntimeError('fake daemon exited during measurement')
        cpu_seconds = (last_ticks - first_ticks) / os.sysconf('SC_CLK_TCK')
        result = {'scope': 'private D-Bus, fake adapters, non-race binary; no real audio or GNOME QA',
                  'window_seconds': elapsed, 'rss_start_kib': first_rss, 'rss_end_kib': last_rss,
                  'cpu_seconds': cpu_seconds, 'cpu_percent_one_core': cpu_seconds / elapsed * 100,
                  'pid': daemon.pid}
        samples = []
        for i in range(105):
            start = time.perf_counter_ns()
            status = subprocess.run([str(binary), 'status', '--json'], capture_output=True, text=True, check=True, timeout=5)
            assert json.loads(status.stdout)['protection_state'] == 'confirmed'
            if i >= 5:
                samples.append((time.perf_counter_ns() - start) / 1e6)
        samples.sort()
        result.update(cli_samples=len(samples), cli_p50_ms=statistics.median(samples), cli_p95_ms=samples[94], cli_max_ms=max(samples))
        result['rss_after_cli_kib'] = stats()[1]
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result))
        assert max(first_rss, last_rss, result['rss_after_cli_kib']) * 1024 < 15_000_000, 'fake RSS budget: <15 MB'
        assert result['cpu_percent_one_core'] < 1, 'fake idle CPU budget: <1% of one core over 60s'
        assert result['cli_p95_ms'] < 15, 'fake CLI p95 budget: <15ms'
    finally:
        daemon.terminate()
        try:
            daemon.wait(timeout=5)
        except subprocess.TimeoutExpired:
            daemon.kill()
            daemon.wait()
