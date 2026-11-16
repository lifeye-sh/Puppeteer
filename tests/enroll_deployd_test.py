import importlib.util
import unittest
import os
import subprocess
import tempfile
import uuid
from pathlib import Path

spec = importlib.util.spec_from_file_location("enroll", Path(__file__).parents[1] / "scripts/enroll-deployd.py")
enroll = importlib.util.module_from_spec(spec)
spec.loader.exec_module(enroll)

class EnrollmentTests(unittest.TestCase):
    def test_data_root_migration_preserves_volumes_and_host_paths(self):
        old = '/var/lib/retired-product'
        model = {'services': {'server': {'environment': {'NT_RUNTIME_PROFILE': old+'/runtime/active.json', 'NT_MP4_DIRECTORY': old+'/mp4', 'KEEP': 'untouched'}, 'volumes': [{'type':'volume','source':'appdata','target':old}, {'type':'bind','source':old+'/host-secret','target':'/run/secrets/key'}]}, 'guacd': {'environment': {'NT_STORAGE_PROFILE':old+'/storage/active.json'}, 'volumes':[{'type':'volume','source':'storageconfig','target':old+'/storage','read_only':True}]}}, 'x-puppeteer-drive': {'recipe': {'environment': {'NT_STORAGE_PROFILE':old+'/storage/active.json'}}}}
        self.assertTrue(enroll.migrate_application_paths(model))
        self.assertEqual(model['services']['server']['volumes'][0], {'type':'volume','source':'appdata','target':'/var/lib/puppeteer'})
        self.assertEqual(model['services']['server']['volumes'][1]['source'],old+'/host-secret')
        self.assertEqual(model['services']['guacd']['environment']['NT_STORAGE_PROFILE'],'/var/lib/puppeteer/storage/active.json')
        self.assertEqual(model['x-puppeteer-drive']['recipe']['environment']['NT_STORAGE_PROFILE'],'/var/lib/puppeteer/storage/active.json')
        self.assertFalse(enroll.migrate_application_paths(model))
    def test_custom_data_root_is_not_silently_moved(self):
        model={'services':{'server':{'environment':{'NT_RUNTIME_PROFILE':'/custom/runtime/active.json'}}}}
        with self.assertRaises(ValueError): enroll.migrate_application_paths(model)
    def model(self):
        return {"name": "puppeteer", "volumes": {"data": {}}, "services": {
            "server": {"image": "pinned", "environment": {"PASSWORD": "a${USER}$$"},
                       "ports": [{"target": 13306, "published": "23306", "host_ip": "127.0.0.1"}]},
            "web": {"image": "web-pinned"}}}
    def test_preserves_and_records_ownership(self):
        model, config, state = enroll.prepare(self.model(), "/opt/puppeteer", "/usr/bin/docker")
        self.assertEqual(state["owned"]["mysql"]["published"], 23306)
        self.assertEqual(model["services"]["server"]["environment"]["PASSWORD"], "a$${USER}$$$$")
        self.assertEqual(model["services"]["web"]["image"], "web-pinned")
        self.assertEqual(config["executor"]["Files"], ["/opt/puppeteer/compose-managed.json"])
        self.assertEqual(len(model["services"]["server"]["volumes"]), 2)
    def test_duplicate_mapping_rejected(self):
        model = self.model()
        model["services"]["server"]["ports"] *= 2
        with self.assertRaises(ValueError):
            enroll.prepare(model, "/opt/puppeteer", "/usr/bin/docker")
    def test_host_network_rejected(self):
        model = self.model()
        model["services"]["server"]["network_mode"] = "host"
        with self.assertRaises(ValueError):
            enroll.prepare(model, "/opt/puppeteer", "/usr/bin/docker")

class GraphicsEnrollmentTests(unittest.TestCase):
    def model(self):
        return {"services":{"server":{"environment":{"NT_GUACD_ADDR":"guacd:4822","NT_GUACD_RELAY_HOST":"server","NT_RDP_DRIVE_ENABLED":"true"},"networks":{"graphics":None}},"guacd":{"image":"graphics:1","networks":{"graphics":None},"healthcheck":{"test":["CMD","health"]}}},"networks":{"graphics":{"internal":True}}}
    def test_adopts_local_recipe_and_preserves_disabled_upgrade(self):
        model=enroll.enroll_graphics(self.model())
        self.assertEqual(model['x-puppeteer-graphics']['driveEnabled'],'true')
        self.assertEqual(model['services']['guacd']['labels']['io.puppeteer.managed.graphics'],'v1')
        model['services']['guacd']['profiles']=['puppeteer-graphics-disabled']
        model['services']['server']['environment']['NT_GUACD_ADDR']=''
        self.assertEqual(enroll.enroll_graphics(model)['services']['guacd']['profiles'],['puppeteer-graphics-disabled'])
        self.assertEqual(model['x-puppeteer-graphics']['driveEnabled'],'true')
    def test_external_or_published_graphics_is_not_adopted(self):
        model=self.model();model['services']['server']['environment']['NT_GUACD_ADDR']='external:4822'
        self.assertNotIn('x-puppeteer-graphics',enroll.enroll_graphics(model))
        model=self.model();model['services']['guacd']['ports']=[{'target':4822,'published':'4822'}]
        self.assertNotIn('x-puppeteer-graphics',enroll.enroll_graphics(model))

class DriveEnrollmentTests(unittest.TestCase):
    def test_adoption_and_disabled_upgrade_preserve_recipe(self):
        model=GraphicsEnrollmentTests().model()
        g=model['services']['guacd']
        g.update({'devices':[{'source':'/dev/fuse','target':'/dev/fuse'}], 'volumes':[{'type':'volume','source':'storageconfig','target':'/var/lib/puppeteer/storage','read_only':True}], 'environment':{'NT_STORAGE_PROFILE':'/var/lib/puppeteer/storage/active.json'}})
        enroll.enroll_graphics(model)
        self.assertEqual(model['x-puppeteer-drive']['version'],1)
        saved=__import__('json').dumps(model['x-puppeteer-drive'],sort_keys=True)
        model['services']['guacd']={'image':'preserved:1'}
        model['x-puppeteer-graphics']['driveEnabled']='false'
        enroll.enroll_graphics(model)
        self.assertEqual(__import__('json').dumps(model['x-puppeteer-drive'],sort_keys=True),saved)
        self.assertEqual(model['x-puppeteer-graphics']['driveEnabled'],'false')

class MP4EnrollmentTests(unittest.TestCase):
    def model(self):
        return {'services':{'server':{'environment':{'NT_MP4_URL':'http://mp4-renderer:8090','NT_MP4_TOKEN_FILE':'/run/secrets/mp4_token','NT_MP4_DIRECTORY':'/var/lib/puppeteer/mp4'},'networks':{'render':None},'depends_on':{'mp4-renderer':{'condition':'service_healthy'},'postgres':{'condition':'service_healthy'}}},'mp4-renderer':{'image':'renderer:1','environment':{'NT_MP4_TOKEN_FILE':'/run/secrets/mp4_token'},'networks':{'render':None},'healthcheck':{'test':['CMD','health']}}},'networks':{'render':{'internal':True}}}
    def test_adoption_and_disabled_upgrade(self):
        model=self.model();enroll.enroll_graphics(model)
        self.assertEqual(model['x-puppeteer-mp4']['version'],1)
        self.assertNotIn('mp4-renderer',model['services']['server']['depends_on'])
        self.assertIn('postgres',model['services']['server']['depends_on'])
        model['services']['server']['environment']['NT_MP4_URL']=''
        model['services']['mp4-renderer']['profiles']=['puppeteer-mp4-disabled']
        enroll.enroll_graphics(model)
        self.assertEqual(model['services']['server']['environment']['NT_MP4_URL'],'')
        self.assertEqual(model['services']['mp4-renderer']['profiles'],['puppeteer-mp4-disabled'])
        self.assertEqual(model['services']['mp4-renderer']['image'],'renderer:1')
    def test_external_renderer_not_adopted(self):
        for change in ('external','ports','dependency'):
            model=self.model()
            if change=='external': model['services']['server']['environment']['NT_MP4_URL']='https://outside'
            elif change=='ports': model['services']['mp4-renderer']['ports']=[{'target':8090,'published':'8090'}]
            else: model['services']['other']={'depends_on':{'mp4-renderer':{}}}
            enroll.enroll_graphics(model)
            self.assertNotIn('x-puppeteer-mp4',model)

class OperationsEnrollmentTests(unittest.TestCase):
    def model(self):
        return {'services': {'server': {'image': 'server'}, 'web': {'image': 'web'}, 'postgres': {'image': 'postgres:18-alpine@sha256:fixture', 'environment': {'POSTGRES_USER': 'puppeteer', 'POSTGRES_DB': 'puppeteer'}, 'volumes': [{'type': 'volume', 'source': 'pgdata', 'target': '/var/lib/postgresql'}]}}}
    def test_standard_adoption_does_not_enable_services(self):
        model=self.model()
        enroll.enroll_graphics(model)
        self.assertEqual(model['x-puppeteer-operations'], {'version':1,'wal':True})
        self.assertNotIn('x-puppeteer-wal',model)
        self.assertEqual(model['services']['postgres']['image'],'postgres:18-alpine@sha256:fixture')
    def test_custom_postgres_is_not_adopted(self):
        for field,value in [('image','custom:18'),('command',['postgres','-c','custom=1']),('ports',[{'target':5432,'published':'5432'}]),('network_mode','host')]:
            model=self.model();model['services']['postgres'][field]=value
            enroll.enroll_operations(model)
            self.assertFalse(model['x-puppeteer-operations']['wal'])
    def test_upgrade_preserves_wal_state_and_original_recipe(self):
        for enabled in (True,False):
            model=self.model()
            model['x-puppeteer-operations']={'version':1,'wal':True}
            model['x-puppeteer-wal']={'image':'original-pinned-image','enabled':enabled}
            model['services']['postgres']['image']='managed-postgres:fixed'
            model['services']['web']['volumes']=[{'type':'bind','source':'/root/private/repository/public','target':'/srv/agent-updates','read_only':True}]
            expected=__import__('copy').deepcopy(model)
            enroll.enroll_operations(model)
            self.assertEqual(model,expected)

@unittest.skipUnless(os.name == 'posix' and os.geteuid() == 0, 'Linux root container required')
class EnrollmentRecoveryTests(unittest.TestCase):
    def test_runtime_directory_survives_private_umask(self):
        with tempfile.TemporaryDirectory(prefix='enroll-mode-') as temporary:
            root=Path(temporary); (root/'config').mkdir()
            source=root/'source.json'
            source.write_text(__import__('json').dumps({'name':'mode-test','services':{'server':{'image':'server'},'web':{'image':'web'}}}))
            script=Path(__file__).parents[1]/'backend/deploy/enroll-deployd.py'
            result=subprocess.run(['bash','-c','umask 077; exec python3 "$1" --source "$2" --directory "$3" --docker /usr/bin/docker','bash',str(script),str(source),str(root)],capture_output=True,text=True)
            self.assertEqual(result.returncode,0,result.stderr)
            runtime=(root/'config/deployd-run').stat()
            self.assertEqual(runtime.st_mode & 0o777,0o750)
            self.assertEqual(runtime.st_gid,10001)
            self.assertEqual((root/'config/deployd-token').stat().st_mode & 0o777,0o640)

    def test_refresh_failure_restores_original_and_allows_retry(self):
        # Minimal Linux test images need the mocked systemd unit directory.
        Path('/etc/systemd/system').mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='enroll-') as temporary:
            root = Path(temporary)
            (root/'config').mkdir()
            bins = root/'bin'; bins.mkdir()
            project = 'enroll-' + uuid.uuid4().hex[:10]
            source = {'name':project, 'services': {'server':{'image':'server'}, 'web':{'image':'web'}}}
            (root/'original.json').write_text(__import__('json').dumps(source))
            wrapper = '#!/bin/bash\nexec docker compose -f "' + str(root/'original.json') + '" "$@"\n'
            (root/'puppeteerctl').write_text(wrapper); (root/'puppeteerctl').chmod(0o700)
            shim = r"""#!/usr/bin/env python3
import os,sys,json
from pathlib import Path
root=Path(os.environ['ENROLL_TEST_ROOT']); args=sys.argv[1:]
with (root/'calls').open('a') as log: log.write(Path(sys.argv[0]).name+' '+ ' '.join(args)+'\n')
if Path(sys.argv[0]).name=='systemctl': sys.exit(0)
if args[0]=='cp':
 p=Path(args[2]);p.write_text('#!/bin/sh\nexit 0\n');sys.exit(0)
if 'ps' in args: print('fixture-server')
if 'config' in args and '--format' in args: print((root/'original.json').read_text())
if 'up' in args and any('compose-managed.json' in a for a in args): sys.exit(12)
"""
            for name in ('docker','systemctl'):
                path=bins/name;path.write_text(shim);path.chmod(0o700)
            env=dict(os.environ, ENROLL_TEST_ROOT=str(root), PATH=str(bins)+':'+os.environ['PATH'])
            script=Path(__file__).parents[1]/'backend/deploy/enroll-deployd.sh'
            for attempt in range(2):
                result=subprocess.run(['bash',str(script),str(root)],env=env,capture_output=True,text=True)
                self.assertNotEqual(result.returncode,0)
                self.assertEqual(result.returncode,12, result.stderr)
                self.assertIn('original services restored automatically', result.stderr)
                self.assertEqual((root/'puppeteerctl').read_text(),wrapper)
                self.assertFalse((root/'config/deployd.json').exists())
                self.assertFalse((root/'compose-managed.json').exists())
                self.assertFalse(Path('/etc/systemd/system/puppeteer-deployd-'+project+'.service').exists())
            calls=(root/'calls').read_text()
            self.assertIn('systemctl disable --now puppeteer-deployd-'+project+'.service',calls)
            self.assertIn(str(root/'original.json')+' up -d',calls)

if __name__ == "__main__":
    unittest.main()
