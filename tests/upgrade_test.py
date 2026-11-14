"""Run in an isolated Linux container: python3 tests/upgrade_test.py (no Docker socket)."""
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / 'scripts/upgrade.sh'
SHIM = r"""#!/usr/bin/env python3
import io,json,os,sys,tarfile
from pathlib import Path
a=sys.argv[1:]; name=Path(sys.argv[0]).name
with open(os.environ['CALLS'],'a') as f: f.write(name+' '+json.dumps(a)+'\n')
mode=os.environ.get('CASE','')
if name=='curl':
 m={'product':'Puppeteer','version':'r17-rc11','platform':'linux/amd64','schemaVersion':107,'images':[{'component':c,'pinnedReference':'cloners/puppeteer-'+c+'@sha256:'+'a'*64} for c in ['server','web']]}
 if mode=='invalid': m['images'][0]['pinnedReference']='evil/image:latest'
 Path(a[a.index('-o')+1]).write_text(json.dumps(m))
elif name=='docker':
 if a[0]=='pull' and mode=='pull-failure': sys.exit(1)
 if a[0]=='inspect':
  print(('cloners/puppeteer-'+('server' if a[-1]=='a'*64 else 'web')+'@sha256:'+'a'*64) if '{{.Config.Image}}' in a else 'sha256:'+'b'*64)
 if a[0]=='run':
  with tarfile.open(fileobj=sys.stdout.buffer,mode='w|') as t:
   data=b'preserved'; entry=tarfile.TarInfo('storage/file'); entry.size=len(data); t.addfile(entry,io.BytesIO(data))
elif name=='puppeteerctl':
 if a[:2]==['ps','--status']: print('postgres\nserver\nweb\nguacd')
 elif a[:2]==['ps','-q']: print(('a' if a[-1]=='server' else 'b')*64)
 elif a[:2]==['config','--images']: print('old-server\nold-web')
 elif a[:3]==['exec','-T','postgres']:
  if any('SELECT schema_version' in v for v in a): print('108' if mode=='downgrade' else '106')
  elif any('pg_dump' in v for v in a):
   if mode=='backup-failure': sys.exit(1)
   print('fixture dump')
  else: print('fixture contents')
 elif a[0]=='up' and mode=='start-failure': sys.exit(1)
"""

@unittest.skipUnless(os.name=='posix' and os.geteuid()==0, 'isolated Linux root container required')
class Upgrade(unittest.TestCase):
 def run_case(self, case='', check=False):
  with tempfile.TemporaryDirectory() as td:
   root=Path(td); deploy=root/'deploy'; config=deploy/'config'; config.mkdir(parents=True)
   original='PUPPETEER_SERVER_IMAGE=old-server\nPUPPETEER_WEB_IMAGE=old-web\nPUPPETEER_ORIGIN=https://custom.example\nPUPPETEER_GRAPHICS_IMAGE=custom-graphics\n'
   (config/'production.env').write_text(original)
   (config/'private-key').write_text('must-not-change')
   (deploy/'compose.yaml').write_text('custom compose must remain')
   binaries=root/'bin'; binaries.mkdir()
   for target in [binaries/'docker',binaries/'curl',deploy/'puppeteerctl']:
    target.write_text(SHIM);target.chmod(0o700)
   calls=root/'calls';calls.write_text('')
   env=dict(os.environ,PATH=str(binaries)+':'+os.environ['PATH'],CASE=case,CALLS=str(calls))
   result=subprocess.run(['bash',str(SCRIPT),'--dir',str(deploy)]+(['--check'] if check else []),env=env,capture_output=True,text=True)
   log=calls.read_text(); actual=(config/'production.env').read_text()
   self.assertEqual((config/'private-key').read_text(),'must-not-change')
   self.assertEqual((deploy/'compose.yaml').read_text(),'custom compose must remain')
   if not case:
    self.assertEqual(result.returncode,0,result.stderr)
    if check:
     self.assertEqual(actual,original);self.assertNotIn('"pull"',log);self.assertNotIn('"stop"',log)
    else:
     self.assertIn('cloners/puppeteer-server@sha256:',actual)
     self.assertIn('PUPPETEER_GRAPHICS_IMAGE=custom-graphics',actual)
     self.assertEqual((deploy/'installed-version').read_text().strip(),'r17-rc11')
     backups=list((deploy/'backups').iterdir());self.assertEqual(len(backups),1)
     self.assertTrue((backups[0]/'SHA256SUMS').is_file())
     self.assertLess(log.index('"pull"'),log.index('"stop"'))
     self.assertIn('"--no-deps"',log)
   else:
    self.assertNotEqual(result.returncode,0)
    self.assertFalse((deploy/'installed-version').exists())
    if case!='start-failure':self.assertEqual(actual,original)
    if case in ['invalid','downgrade','pull-failure']:self.assertNotIn('"stop"',log)
    if case=='start-failure':self.assertEqual(log.count('"stop"'),2)
 def test_success(self): self.run_case()
 def test_check(self): self.run_case(check=True)
 def test_untrusted_manifest(self): self.run_case('invalid')
 def test_downgrade(self): self.run_case('downgrade')
 def test_pull_failure(self): self.run_case('pull-failure')
 def test_backup_failure(self): self.run_case('backup-failure')
 def test_start_failure(self): self.run_case('start-failure')

if __name__=='__main__': unittest.main()
