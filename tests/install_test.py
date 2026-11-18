"""Isolated install.sh flow with fake Docker/network/crypto commands, no Docker socket."""
import os
import shutil
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
  if '-addext' in a:sys.exit('unsupported option: -addext')
  if os.environ.get('TLS_FAIL'):sys.exit('fixture certificate generation failure')
  if os.environ.get('REAL_OPENSSL'):os.execv(os.environ['REAL_OPENSSL'],[os.environ['REAL_OPENSSL'],*a])
  assert '-config' in a and '-extensions' in a
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
 if '.OSType' in a[-1] and os.environ.get('DOCKER_INFO_FAIL'):sys.exit(1)
 print(os.environ.get('DOCKER_INFO','linux 4 8589934592') if '.OSType' in a[-1] else '/var/lib/docker')
"""
@unittest.skipUnless(os.name=='posix' and os.geteuid()==0,'Linux root container required')
class Install(unittest.TestCase):
 def run_install(self, extra, enabled=True, bind='127.0.0.1', auto=False, info='linux 4 8589934592', failure=None, info_fail=False):
  with tempfile.TemporaryDirectory() as td:
   root=Path(td);bins=root/'bin';bins.mkdir();deploy=root/'deployment'
   for name in ['docker','curl','df','openssl','systemctl']:
    p=bins/name;p.write_text(SHIM);p.chmod(0o700)
   env=dict(os.environ,PATH=str(bins)+':'+os.environ['PATH'],AUTO_ENROLL='1' if auto else '',DOCKER_INFO=info,DOCKER_INFO_FAIL='1' if info_fail else '')
   proc=subprocess.run(['bash',str(SCRIPT),'--core','--dir',str(deploy),'--host','db.example.com']+extra,env=env,capture_output=True,text=True)
   if failure:
    self.assertNotEqual(proc.returncode,0)
    self.assertIn(failure,proc.stderr)
    self.assertFalse(deploy.exists(),'Failed preflight must not create deployment')
    return
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
 def test_real_tls_extensions_and_visible_failure(self):
  for host in ['db.example.com','192.168.1.170']:
   for fail in [False,True]:
    with self.subTest(host=host,fail=fail), tempfile.TemporaryDirectory() as td:
     root=Path(td);bins=root/'bin';bins.mkdir();deploy=root/'deployment'
     for name in ['docker','curl','df','openssl','systemctl']:
      p=bins/name;p.write_text(SHIM);p.chmod(0o700)
     real=shutil.which('openssl');self.assertTrue(real)
     env=dict(os.environ,PATH=str(bins)+':'+os.environ['PATH'],REAL_OPENSSL=real,TLS_FAIL='1' if fail else '',AUTO_ENROLL='')
     proc=subprocess.run(['bash',str(SCRIPT),'--core','--dir',str(deploy),'--host',host],env=env,capture_output=True,text=True)
     if fail:
      self.assertNotEqual(proc.returncode,0)
      self.assertIn('fixture certificate generation failure',proc.stderr)
      self.assertIn('TLS certificate generation failed',proc.stderr)
      self.assertTrue((deploy/'config/secrets/vault_key').exists())
      self.assertTrue((deploy/'config/tls-generation.log').exists())
      self.assertFalse((deploy/'puppeteerctl').exists())
      self.assertNotIn('a'*64,proc.stdout+proc.stderr)
     else:
      self.assertEqual(proc.returncode,0,proc.stdout+proc.stderr)
      certificate=deploy/'config/tls/server.crt';key=deploy/'config/tls/server.key'
      value=subprocess.check_output([real,'x509','-in',str(certificate),'-noout','-text'],text=True)
      self.assertIn('DNS:'+host if host[0].isalpha() else 'IP Address:'+host,value)
      self.assertIn('CA:FALSE',value)
      self.assertIn('TLS Web Server Authentication',value)
      cert_pub=subprocess.check_output([real,'x509','-in',str(certificate),'-noout','-pubkey'])
      key_pub=subprocess.check_output([real,'pkey','-in',str(key),'-pubout'])
      self.assertEqual(cert_pub,key_pub)
      self.assertEqual(key.stat().st_mode & 0o777,0o400)
      self.assertFalse((deploy/'config/tls-request.cnf').exists())
      self.assertFalse((deploy/'config/tls-generation.log').exists())
 def test_automatic_enrollment(self):self.run_install([],auto=True)
 def test_defaults(self):self.run_install([])
 def test_remote_bind(self):self.run_install(['--database-proxy-bind','0.0.0.0'],bind='0.0.0.0')
 def test_disable(self):self.run_install(['--no-database-proxy'],enabled=False)
 def test_three_point_five_gib_and_reported_host_are_accepted(self):
  self.run_install([],info='linux 2 3758096384')
  self.run_install([],info='linux 2 3822956544')
  self.run_install([],info='linux 2 4294967296')
 def test_hardware_failures_report_the_specific_limit(self):
  for info,reason in [('linux 2 3758096383','at least 3.5 GiB'),('linux 2 3221225472','at least 3.5 GiB'),('linux 2 2147483648','Memory=2048 MiB'),('linux 1 8589934592','at least 2 CPUs'),('windows 4 8589934592','Linux containers')]:
   with self.subTest(info=info):self.run_install([],info=info,failure=reason)
 def test_invalid_or_unavailable_docker_info_is_rejected(self):
  for info in ['', 'linux missing 8589934592','linux 2 8589934592 unexpected']:
   with self.subTest(info=info):self.run_install([],info=info,failure='invalid CPU/memory information')
  self.run_install([],info_fail=True,failure='Cannot query Docker daemon')
if __name__=='__main__':unittest.main()
