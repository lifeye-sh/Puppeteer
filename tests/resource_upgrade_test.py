"""Resource-aware upgrade must keep budgets and reject incompatible images."""
import json
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / 'scripts/upgrade.sh'
scope = {}
exec(SCRIPT.read_text(encoding='utf-8').split("<<'PYIMAGES'\n", 1)[1].split('\nPYIMAGES', 1)[0], scope)


class ResourceUpgrade(unittest.TestCase):
    def test_database_budget_refuses_older_server(self):
        model=self.model()
        model['x-puppeteer-database-budget']=1
        labels={'io.puppeteer.resources':'v1','io.puppeteer.renderer.streaming':'v1'}
        with patch('subprocess.run', return_value=SimpleNamespace(returncode=0, stdout=json.dumps(labels))):
            with self.assertRaisesRegex(ValueError,'before downtime'): scope['validate_resource_images'](model)
        labels['io.puppeteer.database.budget']='v1'
        with patch('subprocess.run', return_value=SimpleNamespace(returncode=0, stdout=json.dumps(labels))):
            scope['validate_resource_images'](model)

    def model(self):
        return {'services': {
            'server': {'image': 'cloners/puppeteer-server:old', 'environment': {'NT_RESOURCE_PROFILE': 'light', 'GOMEMLIMIT': '512MiB'}, 'mem_limit': 768 << 20},
            'mp4-renderer': {'image': 'cloners/puppeteer-renderer:old', 'profiles': ['puppeteer-mp4-disabled'], 'volumes': [{'source': 'mp4scratch'}], 'environment': {'NT_MP4_SCRATCH_DIRECTORY': '/var/lib/puppeteer-renderer', 'NT_MP4_SCRATCH_QUOTA_MIB': '2048'}}},
            'volumes': {'mp4scratch': {'name': 'puppeteer_mp4scratch'}}}

    def test_upgrade_preserves_profile_disk_quota_and_disabled_renderer(self):
        model = self.model()
        before = json.dumps(model, sort_keys=True).replace(':old', ':new')
        scope['update_images'](model, {'server': 'cloners/puppeteer-server:new', 'renderer': 'cloners/puppeteer-renderer:new'})
        self.assertEqual(json.dumps(model, sort_keys=True), before)
        with patch('subprocess.run', return_value=SimpleNamespace(returncode=0, stdout=json.dumps({'io.puppeteer.resources': 'v1', 'io.puppeteer.renderer.streaming': 'v1'}))) as call:
            scope['validate_resource_images'](model)
            self.assertEqual(call.call_count, 2)  # Includes disabled renderer.

    def test_incompatible_image_refused_before_service_stop(self):
        for labels in ('{}', 'null'):
            with self.subTest(labels=labels), patch('subprocess.run', return_value=SimpleNamespace(returncode=0, stdout=labels)):
                with self.assertRaisesRegex(ValueError, 'before downtime'):
                    scope['validate_resource_images'](self.model())
        source = SCRIPT.read_text(encoding='utf-8')
        self.assertLess(source.index("helper['validate_resource_images'](model)"), source.index('systemctl stop "$deployd_unit"'))

    def test_legacy_install_does_not_require_new_image_labels(self):
        model = self.model()
        del model['services']['server']['environment']['NT_RESOURCE_PROFILE']
        with patch('subprocess.run') as call:
            scope['validate_resource_images'](model)
            call.assert_not_called()

    def test_demand_and_quality_require_new_images_even_without_resource_profile(self):
        for option in ('demand', 'quality'):
            model = self.model()
            env = model['services']['server']['environment']
            del env['NT_RESOURCE_PROFILE']
            if option == 'demand':
                env['NT_MP4_ON_DEMAND'] = 'true'
                model['services']['mp4-renderer']['restart'] = 'no'
            else:
                model['services']['mp4-renderer']['environment']['NT_MP4_QUALITY'] = 'economy'
            with patch('subprocess.run', return_value=SimpleNamespace(returncode=0, stdout='{}')):
                with self.assertRaisesRegex(ValueError, 'before downtime'):
                    scope['validate_resource_images'](model)
            labels = {'io.puppeteer.mp4.ondemand': 'v1', 'io.puppeteer.renderer.lifecycle': 'v1'}
            with patch('subprocess.run', return_value=SimpleNamespace(returncode=0, stdout=json.dumps(labels))) as call:
                scope['validate_resource_images'](model)
                self.assertEqual(call.call_count, 2 if option == 'demand' else 1)
