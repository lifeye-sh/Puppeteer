"""Isolated install.sh flow with fake Docker/network/crypto commands, no Docker socket."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT=Path(__file__).resolve().parents[1]/'scripts/install.sh'
SHIM=r"""#!/usr/bin/env python3
import sys
from pathlib import Path
name=Path(sys.argv[0]).name;a=sys.argv[1:]
if name=='curl':Path(a[a.index('-o')+1]).write_text('fixture configuration')
if name=='df':print('Filesystem 1024-blocks Used Available Capacity Mounted on\nfixture 10000000 0 10000000 0% /')
if name=='openssl':
 if a[0]=='rand':print('a'*64)
 if a[0]=='req':
  Path(a[a.index('-keyout')+1]).write_text('fixture key')
  Path(a[a.index('-out')+1]).write_text('fixture cert')
if name=='docker' and a[0]=='info':
 print('linux 4 8589934592' if '.OSType' in a[-1] else '/var/lib/docker')
"""
@unittest.skipUnless(os.name=='posix' and os.geteuid()==0,'Linux root container required')
class Install(unittest.TestCase):
 def run_install(self, extra, enabled=True, bind='127.0.0.1'):
  with tempfile.TemporaryDirectory() as td:
   root=Path(td);bins=root/'bin';bins.mkdir();deploy=root/'deployment'
   for name in ['docker','curl','df','openssl']:
    p=bins/name;p.write_text(SHIM);p.chmod(0o700)
   env=dict(os.environ,PATH=str(bins)+':'+os.environ['PATH'])
   proc=subprocess.run(['bash',str(SCRIPT),'--core','--dir',str(deploy),'--host','db.example.com']+extra,env=env,capture_output=True,text=True)
   self.assertEqual(proc.returncode,0,proc.stdout+proc.stderr)
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
 def test_defaults(self):self.run_install([])
 def test_remote_bind(self):self.run_install(['--database-proxy-bind','0.0.0.0'],bind='0.0.0.0')
 def test_disable(self):self.run_install(['--no-database-proxy'],enabled=False)
if __name__=='__main__':unittest.main()
