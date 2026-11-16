import importlib.util
from pathlib import Path
import unittest
import json
import tempfile
from unittest.mock import patch
spec=importlib.util.spec_from_file_location('baseline',Path(__file__).resolve().parents[1]/'scripts/performance-baseline.py')
baseline=importlib.util.module_from_spec(spec)
spec.loader.exec_module(baseline)
class Baseline(unittest.TestCase):
    def test_docker_units(self):
        self.assertEqual(baseline.size('1.5GiB'),1610612736)
        self.assertEqual(baseline.size('1.5GB'),1500000000)
        self.assertEqual(baseline.size('0B'),0)
        with self.assertRaises(ValueError): baseline.size('unknown')
    def test_percentile(self):
        self.assertIsNone(baseline.percentile([],.95))
        self.assertEqual(baseline.percentile(list(range(1,101)),.95),95)
    def test_stopped_project_keeps_oom_evidence_without_sampling_other_projects(self):
        def docker(*args):
            if args[0]=='ps': return 'owned\n' if '-a' in args else ''
            if args[0]=='inspect':
                self.assertEqual(args[-1],'owned')
                return '/owned false true 0 none\n'
            self.fail('must not invoke global docker stats for an empty running set')
        with tempfile.TemporaryDirectory() as folder:
            output=Path(folder)/'report.json'
            with patch.object(baseline,'docker',side_effect=docker),patch('sys.argv',['baseline','--project','test-project','--scenario','idle','--seconds','1','--output',str(output)]):
                self.assertEqual(baseline.main(),0)
            report=json.loads(output.read_text())
            self.assertEqual(report['samples'][0]['states'][0]['oomKilled'],'true')
