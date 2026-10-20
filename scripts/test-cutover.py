#!/usr/bin/env python3
"""Exercise the real release scripts with stateful fake executables under /tmp."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

assert os.environ.get('EASYPROFILE_TEST_MODE') == '1', 'test mode required'
REPO = Path(__file__).resolve().parent.parent
UNITS = ('ka1333-input-guard.service', 'easyeffects-output-guard.service')
FAKE = '''#!/usr/bin/python3
import json,os,pathlib,sys
name=pathlib.Path(sys.argv[0]).name; args=sys.argv[1:]
path=pathlib.Path(os.environ['EP_FAKE_STATE']); state=json.loads(path.read_text())
state['calls'].append([name]+args)
result=0; output=''
if name=='systemctl':
 args=[a for a in args if a!='--user']; verb=args[0]; unit=args[-1]
 if verb=='daemon-reload': pass
 elif verb in ('is-active','is-enabled'):
  output=state['units'][unit]['active' if verb=='is-active' else 'enabled']
  if output in ('inactive','disabled','masked','masked-runtime'):result=3
 else:
  if (os.environ.get('EP_FAIL')=='start' and unit=='easyprofiled.service' and verb=='enable' and '--now' in args): result=1
  elif (os.environ.get('EP_FAIL')=='stop' and unit==os.environ.get('EP_FAIL_UNIT') and verb=='stop' and not state.get('failed_once')):
   state['failed_once']=True;result=1
  else:
   u=state['units'][unit]
   if verb in ('start','stop'):u['active']='active' if verb=='start' else 'inactive'
   elif verb=='unmask':
    if u['enabled'].startswith('masked'):u['enabled']='disabled'
   elif verb in ('enable','disable','mask'):
    u['enabled']= {'enable':'enabled','disable':'disabled','mask':'masked'}[verb]+('-runtime' if '--runtime' in args else '')
    if '--now' in args:u['active']='active'
elif name=='easyprofilectl':
 health={'protection_state':'degraded' if os.environ.get('EP_FAIL')=='health' else 'confirmed','last_error':'bad' if os.environ.get('EP_FAIL')=='health' else '', 'observe_only':False,'dsp_bound':True,'monitor_volumes_confirmed':True}
 if os.environ.get('EP_FAIL')=='old-protocol': del health['dsp_bound'];del health['monitor_volumes_confirmed']
 output=json.dumps(health)
 if os.environ.get('EP_FAIL')=='invalid-json':output='broken'
 elif os.environ.get('EP_FAIL')=='status':result=1
path.write_text(json.dumps(state))
if output: print(output)
sys.exit(result)
'''


class CutoverTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='ep-cutover-', dir='/tmp')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo, self.home, self.tools = (self.root / p for p in ('repo', 'home', 'tools'))
        self.home.mkdir(); self.tools.mkdir()
        for name in ('scripts', 'systemd', 'gnome-extension', 'configs'):
            shutil.copytree(REPO/name, self.repo/name)
        (self.repo/'bin').mkdir(); (self.repo/'bin/easyprofilectl').write_text(FAKE)
        (self.repo/'bin/easyprofilectl').chmod(0o755)
        for tool in ('systemctl', 'make', 'gnome-extensions', 'glib-compile-schemas', 'sleep'):
            path = self.tools/tool; path.write_text(FAKE); path.chmod(0o755)
        (self.repo/'test/fixtures').mkdir(parents=True)
        lines=[]
        for i in range(3):
            path=self.home/f'preset-{i}.json'; path.write_text('{}')
            lines.append(hashlib.sha256(b'{}').hexdigest()+f'  ~/preset-{i}.json')
        (self.repo/'test/fixtures/preset-baseline.sha256').write_text('\n'.join(lines)+'\n')
        self.state=self.root/'state.json'
        self.original={u:{'active':'active','enabled':'enabled'} for u in UNITS}
        self.seed()
        self.env=dict(os.environ, HOME=str(self.home), PATH=str(self.tools)+':/usr/bin:/bin',
                      EASYPROFILE_CUTOVER_FAKE_ROOT=str(self.root), EP_FAKE_STATE=str(self.state))

    def seed(self):
        units={u:dict(s) for u,s in self.original.items()}
        units['easyprofiled.service']={'active':'inactive','enabled':'disabled'}
        self.state.write_text(json.dumps({'units':units,'calls':[]}))

    def run_script(self, script, *args, fail=''):
        env=dict(self.env, EP_FAIL=fail)
        return subprocess.run(['bash',str(self.repo/'scripts'/script),*args],env=env,
                              text=True,capture_output=True,timeout=30)

    def assert_restored(self):
        state=json.loads(self.state.read_text())
        self.assertEqual({u:state['units'][u] for u in UNITS}, self.original)
        self.assertEqual(state['units']['easyprofiled.service'],{'active':'inactive','enabled':'disabled'})
        self.assertFalse((self.home/'.local/share/dbus-1/services/org.easyprofile.Daemon.service').exists())
        self.assertIn(['systemctl','--user','stop','easyprofiled.service'], state['calls'])

    def test_start_and_health_failures_restore_both_guards(self):
        for failure in ('start','health','invalid-json','status','old-protocol'):
            with self.subTest(failure=failure):
                backup=self.home/'.local/state/easyprofile/guards-backup.json'
                backup.unlink(missing_ok=True); self.seed()
                result=self.run_script('install.sh',fail=failure)
                self.assertNotEqual(result.returncode,0,result.stdout+result.stderr)
                self.assert_restored()
                if failure in ('health','invalid-json','old-protocol'):
                    calls=json.loads(self.state.read_text())['calls']
                    self.assertEqual(sum(c[0]=='easyprofilectl' for c in calls),1, 'degraded replies must roll back immediately')
                saved=json.loads(backup.read_text())
                self.assertEqual(saved['guards'],self.original)

    def test_negative_and_runtime_states_restored_exactly(self):
        for enabled in ('disabled','masked','masked-runtime','enabled-runtime','static','indirect'):
            with self.subTest(enabled=enabled):
                self.original={UNITS[0]:{'active':'inactive','enabled':enabled},UNITS[1]:{'active':'active','enabled':'disabled'}}
                (self.home/'.local/state/easyprofile/guards-backup.json').unlink(missing_ok=True)
                self.seed()
                result=self.run_script('install.sh',fail='start')
                self.assertNotEqual(result.returncode,0)
                self.assert_restored()

    def test_dry_run_never_writes_builds_or_overwrites_snapshot(self):
        backup=self.home/'.local/state/easyprofile/guards-backup.json'
        backup.parent.mkdir(parents=True);backup.write_bytes(b'original rollback point')
        before={str(p.relative_to(self.home)):p.read_bytes() for p in self.home.rglob('*') if p.is_file()}
        result=self.run_script('install.sh','--dry-run')
        self.assertEqual(result.returncode,0,result.stderr)
        after={str(p.relative_to(self.home)):p.read_bytes() for p in self.home.rglob('*') if p.is_file()}
        self.assertEqual(before,after)
        calls=json.loads(self.state.read_text())['calls']
        self.assertTrue(all(c[0]=='systemctl' and c[2] in ('is-active','is-enabled') for c in calls))
        result=self.run_script('install.sh')
        self.assertNotEqual(result.returncode,0)
        self.assertEqual(backup.read_bytes(),b'original rollback point')

    def test_success_then_rollback_and_hash_mismatch(self):
        result=self.run_script('install.sh')
        self.assertEqual(result.returncode,0,result.stdout+result.stderr)
        activation=self.home/'.local/share/dbus-1/services/org.easyprofile.Daemon.service'
        self.assertIn('--config',activation.read_text());self.assertNotIn('%h',activation.read_text())
        result=self.run_script('rollback.sh')
        self.assertEqual(result.returncode,0,result.stdout+result.stderr);self.assert_restored()
        (self.home/'preset-0.json').write_text('changed fake fixture')
        result=self.run_script('rollback.sh')
        self.assertNotEqual(result.returncode,0)
        self.assertIn('hash mismatch',result.stderr)

    def test_guard_stop_failure_also_rolls_back(self):
        self.env['EP_FAIL_UNIT']=UNITS[1]
        result=self.run_script('install.sh',fail='stop')
        self.assertNotEqual(result.returncode,0,result.stdout+result.stderr)
        self.assert_restored()

    def test_existing_active_daemon_is_preserved_before_installation(self):
        state=json.loads(self.state.read_text())
        state['units']['easyprofiled.service']={'active':'active','enabled':'enabled'}
        self.state.write_text(json.dumps(state))
        result=self.run_script('install.sh')
        self.assertNotEqual(result.returncode,0)
        state=json.loads(self.state.read_text())
        self.assertEqual(state['units']['easyprofiled.service'],{'active':'active','enabled':'enabled'})
        self.assertTrue(all(c[0]=='systemctl' and c[2] in ('is-active','is-enabled') for c in state['calls']))
        self.assertFalse((self.home/'.local/state/easyprofile/guards-backup.json').exists())


if __name__ == '__main__':
    unittest.main()
