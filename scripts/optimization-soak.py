"""Opt-in disposable Linux cgroup v1/v2 aggregate-budget acceptance.

Requires locally built workload/renderer images and cached dependency images.
No published ports, business volumes or credentials.
Privileged helper only manages this run's unique cgroup and its own volume.
"""
import argparse
import json
import secrets
import re
import subprocess
import time
from pathlib import Path
from soak_report import write_report


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--seconds', type=int, default=600)
    parser.add_argument('--renderer-image', default='puppeteer/renderer:optimization-round4')
    parser.add_argument('--load-image', default='puppeteer/soak:optimization')
    parser.add_argument('--graphics-image', default='cloners/puppeteer-graphics:r17-rc13')
    parser.add_argument('--helper-image', default='python:3.13-alpine')
    args = parser.parse_args()
    if not 60 <= args.seconds <= 86400:
        parser.error('duration must be 60..86400 seconds')
    project = 'puppeteer-soak-' + secrets.token_hex(5)
    group = '/' + project
    evidence = Path(__file__).resolve().parents[1] / 'backend/.local' / project
    evidence.mkdir(parents=True)
    containers = []
    token, password = secrets.token_hex(32), secrets.token_hex(24)
    result = {'project': project, 'seconds': args.seconds, 'complete': False,
              'scope': 'isolated application fixture + PostgreSQL + guacd + renderer; not a whole VM or Windows RDP',
              'limits': {'aggregateCPUs': 2, 'aggregateMemoryBytes': 4 * 1024**3}}

    def docker(*argv, check=True, timeout=120):
        p = subprocess.run(['docker', *map(str, argv)], capture_output=True, text=True,
                           encoding='utf-8', errors='replace', timeout=timeout)
        if check and p.returncode:
            raise RuntimeError('docker ' + str(argv[0]) + ' failed: ' + p.stderr[-1500:])
        return (p.stdout + (p.stderr if argv[0] == 'logs' else '')).strip()

    helper = project + '-control'

    def control(code):
        return docker('exec', helper, 'python3', '-c', code, project)

    def resources(inspections):
        targets = json.dumps({c['Name'].rsplit('-', 1)[-1]: c['Id'] for c in inspections})
        return json.loads(docker('exec', helper, 'python3', '-c', """import pathlib,sys,json
r=pathlib.Path('/cg');n=sys.argv[1];v2=(r/'cgroup.controllers').exists()
def read(p):
 try:return p.read_text().strip()
 except FileNotFoundError:return None
def pairs(p):return {k:int(v) for k,v in (line.split() for line in (read(p) or '').splitlines())}
out={}
for name,cid in json.loads(sys.argv[2]).items():
 m=r/n/cid if v2 else r/'memory'/n/cid
 stat=pairs(m/'memory.stat')
 cpu=pairs(m/'cpu.stat') if v2 else {}
 out[name]={'memoryBytes':int(read(m/('memory.current' if v2 else 'memory.usage_in_bytes')) or 0),
 'memoryPeakBytes':int(read(m/('memory.peak' if v2 else 'memory.max_usage_in_bytes')) or 0),
 'rssBytes':stat.get('anon',0) if v2 else stat.get('total_rss',0),
 'cacheBytes':stat.get('file',0) if v2 else stat.get('total_cache',0),
 'cpuNS':cpu.get('usage_usec',0)*1000 if v2 else int(read(r/'cpuacct'/n/cid/'cpuacct.usage') or 0),
 'pids':int(read(m/'pids.current' if v2 else r/'pids'/n/cid/'pids.current') or 0)}
print(json.dumps(out))
""", project, targets))

    def start(name, image, *extra, command=()):
        container = project + '-' + name
        containers.append(container)
        docker('run', '-d', '--pull', 'never', '--name', container,
               '--label', 'io.puppeteer.soak=' + project, '--network', project,
               '--network-alias', name, '--cgroup-parent', group, *extra, image, *command)
        return container

    process_probe_code = (Path(__file__).resolve().parents[1] / 'backend/test/performance/renderer-process-probe.mjs').read_text(encoding='utf-8')

    def process_probe():
        return json.loads(docker('exec', renderer, 'node', '--input-type=module', '-e', process_probe_code))

    try:
        if docker('info', '--format', '{{.OSType}} {{.CgroupDriver}}') != 'linux cgroupfs':
            raise RuntimeError('isolated budget runner requires a Linux Docker daemon with cgroupfs; systemd slices are not managed by this script')
        result['images'] = {}
        for image in [args.load_image, args.renderer_image, args.graphics_image, 'postgres:18-alpine', args.helper_image]:
            result['images'][image] = docker('image', 'inspect', image, '--format', '{{.Id}}')
        docker('network', 'create', '--internal', project)
        docker('volume', 'create', '--label', 'io.puppeteer.soak=' + project, project + '-work')
        docker('run', '-d', '--pull', 'never', '--name', helper, '--network', 'none',
               '--privileged', '--cgroupns', 'host', '-v', '/sys/fs/cgroup:/cg:rw',
               '-v', project + '-work:/work', '--entrypoint', 'sleep',
               args.helper_image, 'infinity')
        version = docker('info', '--format', '{{.CgroupVersion}}')
        result['cgroupVersion'] = version
        control("""import pathlib,sys
name=sys.argv[1]; root=pathlib.Path('/cg'); (pathlib.Path('/work')/'tmp').mkdir(exist_ok=True)
import os;os.chown('/work',10001,10001);os.chown('/work/tmp',10001,10001)
if (root/'cgroup.controllers').exists():
 p=root/name;p.mkdir();(p/'cpu.max').write_text('200000 100000');(p/'memory.max').write_text(str(4*1024**3));(p/'memory.swap.max').write_text('0');(p/'cgroup.subtree_control').write_text('+cpu +memory')
else:
 for controller in ['cpu','memory']:
  p=root/controller/name;p.mkdir()
  if controller=='cpu':(p/'cpu.cfs_period_us').write_text('100000');(p/'cpu.cfs_quota_us').write_text('200000')
  else:
   (p/'memory.limit_in_bytes').write_text(str(4*1024**3))
   if (p/'memory.memsw.limit_in_bytes').exists():(p/'memory.memsw.limit_in_bytes').write_text(str(4*1024**3))
print('aggregate cgroup created')
""")
        database = start('postgres', 'postgres:18-alpine', '--memory', '512m', '--memory-swap', '512m',
                         '-e', 'POSTGRES_USER=soak', '-e', 'POSTGRES_DB=soak', '-e', 'POSTGRES_PASSWORD=' + password)
        for _ in range(60):
            if 'accepting connections' in docker('exec', database, 'pg_isready', '-U', 'soak', check=False):
                break
            time.sleep(1)
        else:
            raise RuntimeError('database readiness timeout')
        # Match the light profile's real PostgreSQL settings.
        for command in ["ALTER SYSTEM SET max_connections='40'", "ALTER SYSTEM SET shared_buffers='128MB'", "ALTER SYSTEM SET work_mem='2MB'", "ALTER SYSTEM SET maintenance_work_mem='32MB'"]:
            docker('exec', database, 'psql', '-U', 'soak', '-d', 'soak', '-v', 'ON_ERROR_STOP=1', '-c', command)
        docker('restart', database)
        for _ in range(60):
            if 'accepting connections' in docker('exec', database, 'pg_isready', '-U', 'soak', check=False): break
            time.sleep(1)
        else:
            raise RuntimeError('database restart readiness timeout')
        renderer = start('renderer', args.renderer_image, '--memory', '1g', '--memory-swap', '1g', '--pids-limit', '160',
                         '--read-only', '--tmpfs', '/tmp:rw,size=256m,mode=1777', '-e', 'NT_MP4_TOKEN=' + token)
        start('guacd', args.graphics_image, '--memory', '512m', '--memory-swap', '512m', '--user', '1000:1000', '--entrypoint', '/opt/guacamole/sbin/guacd', command=('-f','-b','0.0.0.0'))
        load = project + '-load'
        containers.append(load)
        docker('run', '-d', '--pull', 'never', '--name', load, '--network', project, '--network-alias', 'load',
               '--label', 'io.puppeteer.soak=' + project, '--cgroup-parent', group, '--memory', '768m', '--memory-swap', '768m',
               '--pids-limit', '256', '-v', project + '-work:/work', '-e', 'TMPDIR=/work/tmp', '-e', 'GOMEMLIMIT=512MiB', '-e', 'GOMAXPROCS=2',
               '-e', 'NT_SOAK_ISOLATED=1', '-e', 'NT_TEST_RESOURCE_PROFILE=light',
               '-e', 'NT_TEST_DATABASE_URL=postgres://soak:' + password + '@postgres:5432/soak?sslmode=disable',
               '-e', 'NT_MP4_TEST_URL=http://renderer:8090', '-e', 'NT_MP4_TEST_TOKEN=' + token,
               '-e', 'NT_OPTIMIZATION_SOAK_SECONDS=' + str(args.seconds), args.load_image,
               '-test.run', '^TestOptimizationSoak$', '-test.v', '-test.timeout', str(args.seconds + 600) + 's')
        actual = docker('exec', load, 'cat', '/proc/self/cgroup')
        if group not in actual:
            raise RuntimeError('load process is outside aggregate cgroup')
        result['startedUTC'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
        (evidence / 'state.json').write_text(json.dumps(result, indent=2), encoding='utf-8')
        print(json.dumps({'started': project, 'evidence': str(evidence), 'seconds': args.seconds}), flush=True)
        started = time.monotonic()
        last_sample = time.time()
        last_details = -60
        last_report = -240
        with (evidence / 'samples.jsonl').open('w', encoding='utf-8') as samples:
            while True:
                inspections = json.loads(docker('inspect', *containers))
                if any(c['HostConfig']['CgroupParent'] != group for c in inspections):
                    raise RuntimeError('service is outside aggregate cgroup')
                states = [{k: c['State'].get(k) for k in ['Status','ExitCode','OOMKilled','StartedAt','FinishedAt']} | {'name': c['Name'], 'health': c['State'].get('Health', {}).get('Status'), 'restarts': c['RestartCount']} for c in inspections]
                metrics = json.loads(control("""import pathlib,json,sys
r=pathlib.Path('/cg');n=sys.argv[1];v2=(r/'cgroup.controllers').exists()
files={'memoryBytes':r/n/'memory.current','memoryLimit':r/n/'memory.max','cpuMax':r/n/'cpu.max','cpu':r/n/'cpu.stat','oom':r/n/'memory.events'} if v2 else {'memoryBytes':r/'memory'/n/'memory.usage_in_bytes','memoryLimit':r/'memory'/n/'memory.limit_in_bytes','cpuQuota':r/'cpu'/n/'cpu.cfs_quota_us','cpuPeriod':r/'cpu'/n/'cpu.cfs_period_us','cpu':r/'cpu'/n/'cpu.stat','oom':r/'memory'/n/'memory.oom_control','memoryFailures':r/'memory'/n/'memory.failcnt'}
pids=r/n/'pids.current' if v2 else r/'pids'/n/'pids.current'
if pids.exists():files['pidsCurrent']=pids
peak=r/n/'memory.peak' if v2 else r/'memory'/n/'memory.max_usage_in_bytes'
if peak.exists():files['memoryPeakBytes']=peak
print(json.dumps({k:p.read_text().strip() for k,p in files.items()}))
"""))
                sample = {'elapsedSeconds': round(time.monotonic() - started, 1), 'aggregate': metrics, 'containers': states}
                sample['resources'] = resources(inspections)
                if sample['elapsedSeconds'] - last_details >= 60 and all(s['Status'] == 'running' for s in states):
                    sample['rendererProcesses'] = process_probe()
                    last_details = sample['elapsedSeconds']
                now = time.time()
                gap = now - last_sample
                last_sample = now
                if gap > 90 or gap < 0:
                    raise RuntimeError('sampling continuity lost (host sleep, clock change or stalled Docker); restart acceptance')
                samples.write(json.dumps(sample) + '\n'); samples.flush()
                if any(s['OOMKilled'] for s in states): raise RuntimeError('container OOM killed')
                if any(s['restarts'] or s['health'] == 'unhealthy' for s in states): raise RuntimeError('container restarted or became unhealthy')
                killed = re.search(r'^oom_kill\s+(\d+)$', metrics['oom'], re.M)
                if killed and int(killed.group(1)): raise RuntimeError('aggregate cgroup OOM kill')
                if int(metrics['memoryLimit']) != 4 * 1024**3: raise RuntimeError('aggregate memory limit drift')
                if 'cpuQuota' in metrics and int(metrics['cpuQuota']) != 200000: raise RuntimeError('aggregate CPU limit drift')
                if 'cpuPeriod' in metrics and int(metrics['cpuPeriod']) != 100000: raise RuntimeError('aggregate CPU period drift')
                if 'cpuMax' in metrics and metrics['cpuMax'].split() != ['200000','100000']: raise RuntimeError('aggregate CPU limit drift')
                result['peakMemoryBytes'] = max(result.get('peakMemoryBytes', 0), int(metrics['memoryBytes']))
                if 'memoryPeakBytes' in metrics:
                    result['peakCgroupMemoryBytes'] = int(metrics['memoryPeakBytes'])
                result['elapsedSeconds'] = sample['elapsedSeconds']
                load_state = next(s for s in states if s['name'] == '/' + load)
                if load_state['Status'] == 'exited':
                    if load_state['ExitCode'] != 0: raise RuntimeError('workload failed; see load.log')
                    if any(s['Status'] != 'running' for s in states if s['name'] != '/' + load):
                        raise RuntimeError('dependency exited before completion')
                    records = [json.loads(m.group(1)) for m in re.finditer(r'SOAK (\{[^\n]+\})', docker('logs', load))]
                    final = next((r for r in reversed(records) if r.get('complete')), None)
                    if not final or final.get('elapsedSeconds', 0) < args.seconds or final.get('cycles', 0) < 1 or final.get('backups', 0) < 1:
                        raise RuntimeError('workload did not provide complete duration and mixed-load evidence')
                    result['workload'] = final
                    result['finalRendererProcesses'] = process_probe()
                    if result['finalRendererProcesses']['persistentZombies']:
                        raise RuntimeError('renderer has persistent zombies after workload completion')
                    break
                if any(s['Status'] != 'running' for s in states): raise RuntimeError('dependency exited')
                if time.monotonic() - started > args.seconds + 600: raise RuntimeError('workload deadline')
                if int(time.monotonic() - started) % 60 < 10:
                    print(json.dumps({'elapsedSeconds': sample['elapsedSeconds'], 'aggregateMemoryBytes': metrics['memoryBytes']}), flush=True)
                    (evidence / 'state.json').write_text(json.dumps(result, indent=2), encoding='utf-8')
                if sample['elapsedSeconds'] - last_report >= 300:
                    (evidence / 'load.log').write_text(docker('logs', load), encoding='utf-8')
                    write_report(evidence)
                    last_report = sample['elapsedSeconds']
                time.sleep(10)
        result['complete'] = True
        result['elapsedSeconds'] = round(time.monotonic() - started, 1)
    except BaseException as error:
        result['error'] = str(error)
        raise
    finally:
        for container in containers:
            (evidence / (container.rsplit('-', 1)[-1] + '.log')).write_text(docker('logs', container, check=False), encoding='utf-8')
        (evidence / 'state.json').write_text(json.dumps(result, indent=2), encoding='utf-8')
        try:
            write_report(evidence)
        except Exception as error:
            (evidence / 'report-error.txt').write_text(str(error), encoding='utf-8')
        for container in reversed(containers): docker('rm', '-fv', container, check=False)
        # Only remove empty directories from this unique run, after its children.
        try:
            control("""import pathlib,sys
r=pathlib.Path('/cg');n=sys.argv[1]
for root in [r]+list(r.iterdir()):
 p=root/n
 if p.is_dir():
  try:p.rmdir()
  except OSError:pass
""")
        except Exception: pass
        docker('rm', '-fv', helper, check=False)
        docker('network', 'rm', project, check=False)
        docker('volume', 'rm', project + '-work', check=False)
        print(json.dumps(result), flush=True)


if __name__ == '__main__':
    main()
