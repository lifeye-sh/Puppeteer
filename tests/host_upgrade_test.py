"""Host upgrade archive/recovery guards; no Docker or real database required."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from datetime import datetime, timedelta, timezone

SOURCE = Path(__file__).resolve().parents[1] / 'backend/internal/platform/hostupgrade/operations.py'
spec = importlib.util.spec_from_file_location('host_upgrade', SOURCE)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class AutomationGuards(unittest.TestCase):
    def test_revoked_expired_or_changed_authorization_blocks_execution(self):
        with tempfile.TemporaryDirectory() as directory:
            d = module.Operations.__new__(module.Operations)
            d.state = Path(directory)
            future = (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat()
            past = (datetime.now(timezone.utc) - timedelta(hours=1)).isoformat()
            d.plan = dict(automationRevision=2, actor='admin', startBefore=future)
            base = dict(actor='admin', policy=dict(revision=2, mode='window', expiresAt=future))
            module.json_write(d.state / 'automation.json', base)
            d.check_automation()
            for change in [dict(actor='other'), dict(suspended=True),
                           dict(policy=dict(base['policy'], revision=3)),
                           dict(policy=dict(base['policy'], mode='off')),
                           dict(policy=dict(base['policy'], expiresAt=past))]:
                with self.subTest(change=change):
                    module.json_write(d.state / 'automation.json', dict(base, **change))
                    with self.assertRaises(module.Rejected):
                        d.check_automation()
            module.json_write(d.state / 'automation.json', base)
            d.plan['startBefore'] = past
            with self.assertRaises(module.Rejected):
                d.check_automation()


class ArchiveTests(unittest.TestCase):
    def test_previous_stopped_optional_image_does_not_block_next_upgrade(self):
        service = {'image': 'new-pinned-image', 'profiles': ['puppeteer-mp4-disabled']}
        current = {'Config': {'Image': 'previous-pinned-image'}, 'State': {'Running': False}}
        module.check_component_state(service, current)
        current['State']['Running'] = True
        with self.assertRaises(module.Rejected):
            module.check_component_state(service, current)
        service['profiles'] = []
        with self.assertRaises(module.Rejected):
            module.check_component_state(service, current)

    def test_unsafe_archive_members_are_rejected(self):
        for name, kind in [('../escape', tarfile.REGTYPE), ('/absolute', tarfile.REGTYPE),
                           ('link', tarfile.SYMTYPE), ('hard', tarfile.LNKTYPE),
                           ('device', tarfile.CHRTYPE), ('fifo', tarfile.FIFOTYPE)]:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / 'archive.tar'
                with tarfile.open(path, 'w') as archive:
                    entry = tarfile.TarInfo(name)
                    entry.type = kind
                    entry.linkname = '../../outside'
                    archive.addfile(entry)
                with self.assertRaises(module.Rejected):
                    module.safe_archive(path)

    def test_regular_archive_and_private_atomic_write(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / 'archive.tar'
            with tarfile.open(path, 'w') as archive:
                entry = tarfile.TarInfo('./nested/value')
                entry.size = 3
                archive.addfile(entry, io.BytesIO(b'abc'))
            module.safe_archive(path)
            module.atomic(root / 'private', b'original')
            module.atomic(root / 'private', b'updated')
            self.assertEqual((root / 'private').read_bytes(), b'updated')
            if sys.platform != 'win32':
                self.assertEqual((root / 'private').stat().st_mode & 0o777, 0o600)
            self.assertEqual(list(root.glob('.upgrade-*')), [])


class RestoreGuards(unittest.TestCase):
    def fixture(self, root):
        d = module.Operations.__new__(module.Operations)
        d.root = root
        d.job = root / 'job'
        d.job.mkdir()
        (root / 'config').mkdir()
        d.compose = root / 'compose-managed.json'
        d.binary = root / 'config/puppeteer-deployd'
        d.plan = {'id': 'a' * 32, 'layoutDigest': 'b' * 64}
        d.recipe_path = d.job / 'recipe.json'
        recipe = dict(d.plan, volumes=['fixture-volume'], user='fixture', database='fixture')
        module.json_write(d.recipe_path, recipe)
        for name in ('database.dump', 'compose.original', 'env.original', 'executor.original', 'previous.json'):
            (d.job / name).write_bytes(b'original')
        for name in ('configuration.tar', 'volume-0.tar'):
            with tarfile.open(d.job / name, 'w') as archive:
                entry = tarfile.TarInfo('safe/value')
                entry.size = 3
                archive.addfile(entry, io.BytesIO(b'old'))
        names = [p.name for p in d.job.iterdir()]
        module.json_write(d.job / 'checksums.json', {name: module.digest(d.job / name) for name in names})
        calls = []
        d.cleanup_helpers = lambda: calls.append('cleanup')
        d.stop = lambda recipe: calls.append('stop')
        def ctl(*args, **kwargs):
            calls.append(' '.join(args))
            return b''
        d.ctl = ctl
        def volume(recipe, name, args, **kwargs):
            self.assertEqual(name, 'fixture-volume')
            calls.append('volume ' + args[0])
            return b''
        d.volume_command = volume
        return d, calls

    def test_corrupt_backup_stops_before_database_or_volume_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            d, calls = self.fixture(Path(directory))
            (d.job / 'database.dump').write_bytes(b'tampered')
            with self.assertRaises(module.Rejected):
                d.restore()
            self.assertEqual(calls, ['cleanup'])
            self.assertFalse(d.compose.exists())

    def test_unknown_manifest_paths_cannot_select_restore_targets(self):
        with tempfile.TemporaryDirectory() as directory:
            d, calls = self.fixture(Path(directory))
            checksums = json.loads((d.job / 'checksums.json').read_text())
            checksums['../../other-project'] = hashlib.sha256(b'').hexdigest()
            module.json_write(d.job / 'checksums.json', checksums)
            with self.assertRaises(module.Rejected):
                d.restore()
            self.assertEqual(calls, ['cleanup'])

    def test_restore_validates_then_stops_then_recreates_matching_set(self):
        with tempfile.TemporaryDirectory() as directory:
            d, calls = self.fixture(Path(directory))
            d.restore()
            self.assertLess(next(i for i, c in enumerate(calls) if 'pg_restore --list' in c), calls.index('stop'))
            drop = next(i for i, c in enumerate(calls) if 'dropdb' in c)
            restore = next(i for i, c in enumerate(calls) if 'pg_restore --exit-on-error' in c)
            self.assertLess(calls.index('stop'), drop)
            self.assertLess(drop, restore)
            self.assertLess(restore, calls.index('volume find'))
            self.assertLess(calls.index('volume find'), calls.index('volume tar'))
            self.assertEqual(d.binary.read_bytes(), b'original')

    def test_unexpected_errors_do_not_print_secrets(self):
        p = subprocess.run([sys.executable, str(SOURCE)], input=json.dumps({'operation': 'prepare', 'credential': 'must-not-leak'}),
                           capture_output=True, text=True)
        self.assertNotEqual(p.returncode, 0)
        self.assertNotIn('must-not-leak', p.stdout + p.stderr)
        self.assertFalse(json.loads(p.stdout)['passed'])


if __name__ == '__main__':
    unittest.main()
