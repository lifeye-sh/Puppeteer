"""Isolated install.sh flow with fake Docker/network/crypto commands, no Docker socket."""
import os
from pathlib import Path
import subprocess
import tempfile
import uuid
import unittest

SCRIPT=Path(__file__).resolve().parents[1]/'scripts/install.sh'
SHIM=r"""#!/usr/bin/env python3
import sys,os
from pathlib import Path
name=Path(sys.argv[0]).name;a=sys.argv[1:]
if name=='curl':
 if a[a.index('-o')-1].endswith('.apparmor'): sys.exit('Profile is unavailable at the pinned image revision')
 Path(a[a.index('-o')+1]).write_text('fixture configuration')
if name=='df':print('Filesystem 1024-blocks Used Available Capacity Mounted on\nfixture 10000000 0 10000000 0% /')
if name=='openssl':
 if a[0]=='rand':print('a'*64)
 if a[0]=='req':
  Path(a[a.index('-keyout')+1]).write_text('fixture key')
  Path(a[a.index('-out')+1]).write_text('fixture cert')
if name=='docker' and '/app/puppeteer-deployd' in a and 'test' in a: sys.exit(0 if os.environ.get('AUTO_ENROLL') else 1)
if name=='docker' and a[0]=='cp' and '/app/deployment/.' in a[1]:
 target=Path(a[2]);target.mkdir(exist_ok=True)
 (target/'enroll-deployd.sh').write_text('#!/bin/bash\ntouch "$1/auto-enrolled"\n')
if name=='docker' and 'ps' in a and '-q' in a: print('a'*64)

if name=='docker' and a[0]=='info' and 'SecurityOptions' in a[-1]:
 print('["name=apparmor"]' if os.environ.get('DRIVE_TEST') else '[]');sys.exit(0)
if name=='apparmor_parser':
 with open(os.environ['DRIVE_CALLS'],'a') as f:f.write(' '.join(a)+'\n')
if name=='docker' and a[0]=='info':
 print('linux 4 8589934592' if '.OSType' in a[-1] else '/var/lib/docker')
"""
@unittest.skipUnless(os.name=='posix' and os.geteuid()==0,'Linux root container required')
class Install(unittest.TestCase):
 def run_install(self, extra, enabled=True, bind='127.0.0.1', auto=False):
  with tempfile.TemporaryDirectory() as td:
   root=Path(td);bins=root/'bin';bins.mkdir();deploy=root/'deployment'
   for name in ['docker','curl','df','openssl','systemctl']:
    p=bins/name;p.write_text(SHIM);p.chmod(0o700)
   env=dict(os.environ,PATH=str(bins)+':'+os.environ['PATH'],AUTO_ENROLL='1' if auto else '')
   proc=subprocess.run(['bash',str(SCRIPT),'--core','--dir',str(deploy),'--host','db.example.com']+extra,env=env,capture_output=True,text=True)
   self.assertEqual(proc.returncode,0,proc.stdout+proc.stderr)
   self.assertEqual((deploy/'auto-enrolled').exists(),auto)
   wrapper=(deploy/'puppeteerctl').read_text()
   if enabled:
    overlay=(deploy/'database-proxy.yaml').read_text()
    self.assertIn('NT_DATABASE_PROXY_NAME: "db.example.com"',overlay)
    self.assertIn('"'+bind+':13306:13306"',overlay)
    self.assertIn('"'+bind+':15432:15432"',overlay)
    self.assertIn(str(deploy/'database-proxy.yaml'),wrapper)
   else:
    self.assertFalse((deploy/'database-proxy.yaml').exists())
    self.assertNotIn('database-proxy.yaml',wrapper)
 def test_full_install_automatically_loads_scoped_apparmor(self):
  if not Path('/dev/fuse').exists():self.skipTest('FUSE device required for installer host capability check')
  with tempfile.TemporaryDirectory() as td:
   root=Path(td);bins=root/'bin';bins.mkdir();deploy=root/'deployment';project='drive-install-'+uuid.uuid4().hex[:8]
   profile=Path('/etc/apparmor.d')/('puppeteer-rdp-storage-'+project)
   for name in ['docker','curl','df','openssl','systemctl','apparmor_parser']:
    p=bins/name;p.write_text(SHIM);p.chmod(0o700)
   env=dict(os.environ,PATH=str(bins)+':'+os.environ['PATH'],DRIVE_TEST='1',DRIVE_CALLS=str(root/'calls'),DRIVE_TEMPLATE=str(SCRIPT.parent.parent/'deploy/production/puppeteer-rdp-storage.apparmor'))
   profile.parent.mkdir(exist_ok=True)
   try:
    proc=subprocess.run(['bash',str(SCRIPT),'--dir',str(deploy),'--project',project,'--host','db.example.com'],env=env,capture_output=True,text=True)
    self.assertEqual(proc.returncode,0,proc.stdout+proc.stderr)
    self.assertEqual((deploy/'config/drive-profile.template').read_text(),Path(env['DRIVE_TEMPLATE']).read_text())
    self.assertIn('graphics-apparmor.yaml',(deploy/'puppeteerctl').read_text())
    self.assertIn(profile.name,(deploy/'graphics-apparmor.yaml').read_text())
    self.assertIn('profile '+profile.name,profile.read_text())
    self.assertIn('--skip-kernel-load --skip-cache',(root/'calls').read_text())
    self.assertIn('-r '+str(profile),(root/'calls').read_text())
   finally:
    profile.unlink(missing_ok=True)
 def test_automatic_enrollment(self):self.run_install([],auto=True)
 def test_defaults(self):self.run_install([])
 def test_remote_bind(self):self.run_install(['--database-proxy-bind','0.0.0.0'],bind='0.0.0.0')
 def test_disable(self):self.run_install(['--no-database-proxy'],enabled=False)
if __name__=='__main__':unittest.main()
