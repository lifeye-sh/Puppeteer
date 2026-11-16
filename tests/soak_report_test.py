import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('soak_report', Path(__file__).resolve().parents[1]/'scripts/soak_report.py')
report = importlib.util.module_from_spec(spec)
spec.loader.exec_module(report)


class SoakReportTests(unittest.TestCase):
    def fixture(self, growth=False):
        samples, cycles = [], []
        for at in range(600, 7200, 60):
            memory = 100 if at < 3600 or not growth else 240
            samples.append({'elapsedSeconds': at, 'containers': [{'Status':'running'}],
                'resources': {'load': {'memoryBytes': (memory+at/60)*report.MIB,
                    'rssBytes': memory*report.MIB, 'cacheBytes': at/60*report.MIB,
                    'cpuNS': at*100000000, 'pids': 20}},
                'rendererProcesses': {'zombies':0}})
            cycles.append({'elapsedSeconds':at,'cycles':at//60,'heapBytes':4*report.MIB,
                'goroutines':17,'cycleMS':20000,'poolTotal':5,'poolCanceled':0})
        return samples, cycles

    def test_cache_growth_is_not_reported_as_heap_leak(self):
        samples, cycles = self.fixture()
        result = report.analyze(samples, cycles, {'seconds':7200,'complete':True})
        self.assertEqual(result['trendAssessment'], 'no_growth_flag_observed')
        self.assertEqual(result['findings'], [])

    def test_anonymous_memory_growth_requires_review(self):
        samples, cycles = self.fixture(True)
        result = report.analyze(samples, cycles, {'seconds':7200,'complete':True})
        self.assertEqual(result['trendAssessment'], 'needs_review')
        self.assertIn('匿名内存', result['findings'][0])

    def test_transient_and_persistent_zombies_are_distinguished(self):
        samples,cycles=self.fixture()
        for sample in samples:
            sample['rendererProcesses']={'zombies':1,'persistentZombies':0}
        self.assertEqual(report.analyze(samples,cycles,{})['findings'],[])
        samples[-1]['rendererProcesses']['persistentZombies']=1
        self.assertTrue(any('两秒复查' in f for f in report.analyze(samples,cycles,{})['findings']))

    def test_gradual_heap_growth_below_absolute_threshold_is_flagged(self):
        samples,cycles=self.fixture()
        more_samples,more_cycles=[],[]
        for shift in [0,7200,14400]:
            for sample in samples:
                more_samples.append({**sample,'elapsedSeconds':sample['elapsedSeconds']+shift})
            for cycle in cycles:
                at=cycle['elapsedSeconds']+shift
                more_cycles.append({**cycle,'elapsedSeconds':at,'heapBytes':(4+at//3600)*report.MIB})
        self.assertTrue(any('连续多小时' in f for f in report.analyze(more_samples,more_cycles,{})['findings']))

    def test_short_run_is_not_sufficient_and_artifacts_are_written(self):
        samples, cycles = self.fixture()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            (path/'state.json').write_text(json.dumps({'complete':False,'seconds':36000}))
            (path/'samples.jsonl').write_text(json.dumps(samples[0])+'\n')
            (path/'load.log').write_text('SOAK '+json.dumps(cycles[0])+'\n')
            result = report.write_report(path)
            self.assertFalse(result['runComplete'])
            self.assertEqual(result['trendAssessment'], 'insufficient_duration')
            self.assertTrue((path/'resource-report.html').exists())
            self.assertIn('cpuPercentOneCore',(path/'resources.csv').read_text(encoding='utf-8-sig'))


if __name__ == '__main__':
    unittest.main()
