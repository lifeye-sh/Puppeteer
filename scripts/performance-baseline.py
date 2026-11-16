"""Read-only, project-scoped Docker sampler. Run the workload separately.
Does not read environment variables, record credentials, or change containers.
"""
import argparse
import json
import math
from pathlib import Path
import re
import subprocess
import time
from datetime import datetime, timezone
import urllib.request
from urllib.parse import urlsplit

def docker(*args):
    return subprocess.run(['docker', *args], check=True, capture_output=True, text=True, timeout=20).stdout

def size(value):
    match = re.fullmatch(r'([\d.]+)\s*([KMGT]?i?B)', value.strip())
    if not match: raise ValueError('unsupported Docker size')
    number, unit = match.groups()
    powers = {'B': 1, **{u+'B': 1000**i for i,u in enumerate('KMGT', 1)}, **{u+'iB': 1024**i for i,u in enumerate('KMGT', 1)}}
    return round(float(number)*powers[unit])

def percentile(values, p):
    return sorted(values)[max(0, math.ceil(len(values)*p)-1)] if values else None

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--project', required=True)
    parser.add_argument('--scenario', required=True, help='label only; does not create traffic')
    parser.add_argument('--seconds', type=int, default=120)
    parser.add_argument('--interval', type=int, default=5)
    parser.add_argument('--url', help='optional read-only HTTP GET probe; no credentials/query; TLS verified')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'[a-z][a-z0-9-]{2,40}',args.project) or args.seconds<1 or args.interval<1: parser.error('invalid sampling limits or project')
    if args.url:
        u=urlsplit(args.url)
        if u.scheme not in ('http','https') or not u.hostname or u.username or u.password or u.query or u.fragment: parser.error('probe must be an HTTP(S) URL without credentials, query or fragment')
    report={'version':1,'project':args.project,'scenario':args.scenario,'started':datetime.now(timezone.utc).isoformat(),'requestedSeconds':args.seconds,'complete':False,'samples':[]}
    start=time.monotonic()
    try:
        while time.monotonic()-start<args.seconds:
            tick=time.monotonic()
            sample={'seconds':round(tick-start,3),'containers':[]}
            try:
                all_ids=docker('ps','-a','--filter','label=com.docker.compose.project='+args.project,'-q').split()
                ids=docker('ps','--filter','label=com.docker.compose.project='+args.project,'-q').split()
                if not all_ids: raise RuntimeError('project not found')
                for line in (docker('stats','--no-stream','--format','{{json .}}',*ids).splitlines() if ids else []):
                    row=json.loads(line); memory,limit=row['MemUsage'].split('/')
                    sample['containers'].append({'name':row['Name'],'cpuPercent':float(row['CPUPerc'].rstrip('%')),'memoryBytes':size(memory),'limitBytes':size(limit),'blockIO':row['BlockIO'],'networkIO':row['NetIO'],'pids':int(row['PIDs'])})
                # Inspect only state fields. Full inspection includes secrets.
                sample['states']=[dict(zip(('name','running','oomKilled','restartCount','health'),line.split())) for line in docker('inspect','--format','{{.Name}} {{.State.Running}} {{.State.OOMKilled}} {{.RestartCount}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}',*all_ids).splitlines()]
            except (subprocess.SubprocessError,ValueError,KeyError,RuntimeError): sample['error']='docker_sample_failed'
            if args.url:
                at=time.monotonic()
                try:
                    with urllib.request.urlopen(args.url,timeout=5) as response: sample['httpStatus']=response.status
                except Exception: sample['httpError']='probe_failed'
                sample['httpMS']=round((time.monotonic()-at)*1000,2)
            report['samples'].append(sample)
            time.sleep(max(0,min(args.interval-(time.monotonic()-tick),args.seconds-(time.monotonic()-start))))
        report['complete']=True
    except KeyboardInterrupt: pass
    finally:
        report['elapsedSeconds']=round(time.monotonic()-start,3)
        report['samplingErrors']=sum('error' in s for s in report['samples'])
        report['httpErrors']=sum('httpError' in s for s in report['samples'])
        times=[s['httpMS'] for s in report['samples'] if 'httpStatus' in s]
        report['httpP95MS']=percentile(times,.95)
        report['peakObservedMemoryBytes']=max((sum(c['memoryBytes'] for c in s['containers']) for s in report['samples']),default=0)
        args.output.parent.mkdir(parents=True,exist_ok=True)
        args.output.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
        print(json.dumps({k:v for k,v in report.items() if k!='samples'},ensure_ascii=False))
    return 0 if report['complete'] and not report['samplingErrors'] and not report['httpErrors'] else 1

if __name__=='__main__': raise SystemExit(main())
