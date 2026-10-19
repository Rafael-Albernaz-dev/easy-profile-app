#!/usr/bin/env python3
"""Validate release snapshots, preset hashes and daemon health; never control audio."""
import hashlib
import json
import os
from pathlib import Path
import sys
import tempfile
from datetime import datetime, timezone

UNITS = ('ka1333-input-guard.service', 'easyeffects-output-guard.service')
ENABLED = {'enabled', 'enabled-runtime', 'disabled', 'masked', 'masked-runtime', 'static', 'indirect'}


def snapshot(path):
    data = json.loads(Path(path).read_text())
    if data.get('version') != 1 or set(data.get('guards', {})) != set(UNITS):
        raise ValueError('invalid guard snapshot schema')
    for unit in UNITS:
        state = data['guards'][unit]
        if state.get('active') not in ('active', 'inactive') or state.get('enabled') not in ENABLED:
            raise ValueError(f'unsupported saved state for {unit}')
    return data


def main():
    command, *args = sys.argv[1:]
    if command == 'save':
        path = Path(args[0])
        states = args[1:]
        data = {'version': 1, 'backup_timestamp': datetime.now(timezone.utc).isoformat(),
                'guards': {unit: dict(zip(('active', 'enabled'), states[i*2:i*2+2]))
                           for i, unit in enumerate(UNITS)}}
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        fd, temporary = tempfile.mkstemp(prefix='.guards-', dir=path.parent)
        try:
            with os.fdopen(fd, 'w') as stream:
                json.dump(data, stream, indent=2)
                stream.flush()
                os.fsync(stream.fileno())
            snapshot(temporary)
            os.link(temporary, path)  # Atomic; refuse to replace an earlier rollback point.
            directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            os.unlink(temporary)
    elif command == 'read':
        for unit, state in snapshot(args[0])['guards'].items():
            print(unit, state['active'], state['enabled'], sep='\t')
    elif command == 'hashes':
        lines = Path(args[0]).read_text().splitlines()
        if len(lines) != 3:
            raise ValueError('exactly three baseline presets required')
        for line in lines:
            expected, name = line.split(None, 1)
            path = Path(name).expanduser()
            if hashlib.sha256(path.read_bytes()).hexdigest() != expected:
                raise ValueError(f'preset hash mismatch: {path.name}')
        print('All three preset hashes match baseline.')
    elif command == 'health':
        data = json.load(sys.stdin)
        if data.get('protection_state') != 'confirmed' or data.get('last_error') != '' or data.get('observe_only') is not False:
            raise ValueError('daemon is not confirmed, healthy and in control mode')
        if data.get('dsp_bound') is not True or data.get('monitor_volumes_confirmed') is not True:
            raise ValueError('daemon has not confirmed the remediated DSP contract')
    else:
        raise ValueError('unknown release state command')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError) as error:
        sys.exit(str(error))
