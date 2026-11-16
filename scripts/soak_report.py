"""Offline resource trends for an isolated soak; no external packages or network."""
import argparse
import csv
import html
import json
import re
from collections import defaultdict
from pathlib import Path
from statistics import median

MIB = 1024**2


def percentile(values, fraction):
    values = sorted(values)
    return values[min(len(values)-1, int((len(values)-1)*fraction))] if values else None


def analyze(samples, cycles, state):
    active = [s for s in samples if all(c['Status'] == 'running' for c in s.get('containers', []))]
    hours = defaultdict(lambda: defaultdict(list))
    peak, last = {}, {}
    totals, previous = defaultdict(lambda: defaultdict(list)), {}
    for sample in active:
        at = sample['elapsedSeconds']
        for name, item in sample.get('resources', {}).items():
            key = int(at//3600)
            if at >= 600:
                hours[key][name].append((at, item))
            peak[name] = max(peak.get(name, 0), item['memoryBytes'])
            last[name] = item
            for field in ['memoryBytes','rssBytes','cacheBytes','pids']:
                totals[name][field].append(item[field])
            totals[name]['memoryPeakBytes'].append(item.get('memoryPeakBytes') or item['memoryBytes'])
            old = previous.get(name)
            if old and at > old[0] and item['cpuNS'] >= old[1]:
                totals[name]['cpuPercent'].append((item['cpuNS']-old[1])/1e9/(at-old[0])*100)
            previous[name] = (at,item['cpuNS'])
    hourly = []
    for hour, services in sorted(hours.items()):
        values = {}
        for name, rows in services.items():
            values[name] = {'samples': len(rows), 'spanSeconds': rows[-1][0]-rows[0][0],
                'rssP10MiB': round(percentile([v['rssBytes'] for _, v in rows], .1)/MIB, 2),
                'rssMedianMiB': round(median(v['rssBytes'] for _, v in rows)/MIB, 2),
                'cacheMedianMiB': round(median(v['cacheBytes'] for _, v in rows)/MIB, 2),
                'pidsP10': percentile([v['pids'] for _, v in rows], .1)}
        group = [c for c in cycles if int(c['elapsedSeconds']//3600) == hour and c['elapsedSeconds'] >= 600]
        row = {'hour': hour, 'services': values}
        if group:
            row['application'] = {'cycles': len(group), 'heapP10MiB': round(percentile([c['heapBytes'] for c in group], .1)/MIB, 2),
                'goroutinesMedian': median(c['goroutines'] for c in group),
                'heapObjectsMedian': median(c.get('heapObjects', 0) for c in group),
                'gcCountLast': group[-1].get('gcCount'),
                'heapInuseP10MiB': round(percentile([c.get('heapInuseBytes',0) for c in group],.1)/MIB,2),
                'cycleP95MS': percentile([c['cycleMS'] for c in group], .95),
                'poolMaxObserved': max(c['poolTotal'] for c in group),
                'poolCanceled': max(c['poolCanceled'] for c in group)}
        hourly.append(row)
    findings = []
    # Exclude partial final buckets from growth comparisons, and warm-up from all buckets.
    comparable = [h for h in hourly if h['services'] and min(v['spanSeconds'] for v in h['services'].values()) >= 2400]
    if len(comparable) >= 2:
        first, end = comparable[0], comparable[-1]
        for name in first['services'].keys() & end['services'].keys():
            before, after = first['services'][name], end['services'][name]
            if after['rssP10MiB'] > before['rssP10MiB']*1.35+32:
                findings.append(f'{name} 匿名内存低位增长：{before["rssP10MiB"]} → {after["rssP10MiB"]} MiB，需复核')
            if after['pidsP10'] > before['pidsP10']+8:
                findings.append(f'{name} 进程/线程低位增长，需复核')
        a, b = first.get('application', {}), end.get('application', {})
        if a and b:
            if b['heapP10MiB'] > a['heapP10MiB']*1.5+16:
                findings.append('应用 Go 堆低位持续增长，需结合 GC 次数、对象数和堆分析复核')
            if b['goroutinesMedian'] > a['goroutinesMedian']+5:
                findings.append('应用协程数量增长，需复核未释放连接或后台任务')
    process_samples = [s['rendererProcesses'] for s in active if 'rendererProcesses' in s]
    confirmed = [p for p in process_samples if 'persistentZombies' in p]
    if any(p['persistentZombies'] for p in confirmed):
        findings.append('同一渲染器僵尸进程经两秒复查仍存在，需复核')
    if sum(p['zombies'] > 0 for p in process_samples if 'persistentZombies' not in p) >= 3:
        findings.append('至少三次采样发现渲染器僵尸进程，需复核是否持续积累')
    heap_trend = [h['application']['heapP10MiB'] for h in comparable if 'application' in h]
    if len(heap_trend)>=4 and heap_trend[-1]-heap_trend[0]>4 and sum(b>a for a,b in zip(heap_trend,heap_trend[1:]))>=.75*(len(heap_trend)-1):
        findings.append('应用堆低位连续多小时增长，即使未超过绝对阈值也需要复核')
    if any(c.get('poolCanceled', 0) for c in cycles):
        findings.append('数据库连接池出现取消等待，需复核资源竞争')
    adequate = len(comparable) >= 2
    summary = {name: {'memoryMedianMiB':round(median(v['memoryBytes'])/MIB,2),
        'memoryPeakMiB':round(max(v['memoryBytes'])/MIB,2), 'rssMedianMiB':round(median(v['rssBytes'])/MIB,2),
        'memoryHighWaterMiB':round(max(v['memoryPeakBytes'])/MIB,2),
        'cacheMedianMiB':round(median(v['cacheBytes'])/MIB,2),
        'cpuMeanPercentOneCore':round(sum(v['cpuPercent'])/len(v['cpuPercent']),2) if v['cpuPercent'] else None,
        'cpuPeakPercentOneCore':round(max(v['cpuPercent']),2) if v['cpuPercent'] else None,
        'pidsPeak':max(v['pids'])} for name,v in totals.items()}
    return {'runComplete': state.get('complete', False), 'requestedSeconds': state.get('seconds'),
        'observedSeconds': max((s['elapsedSeconds'] for s in samples), default=0),
        'trendAssessment': 'needs_review' if findings else 'no_growth_flag_observed' if adequate else 'insufficient_duration',
        'findings': findings, 'hourly': hourly,
        'peakContainerMemoryMiB': {k: round(v/MIB, 2) for k, v in peak.items()},
        'lastResources': last, 'maxRendererZombies': max((p['zombies'] for p in process_samples), default=None),
        'rendererProcessSamples':len(process_samples),
        'persistentZombieSamples':sum(p['persistentZombies']>0 for p in confirmed),
        'finalRendererProcesses':state.get('finalRendererProcesses'),
        'maxRendererOpenFDs':max((p.get('openFDs',0) for p in process_samples),default=None),
        'cycleCount': max((c['cycles'] for c in cycles), default=0), 'containerSummary':summary,
        'aggregateHighWaterMiB':round(state['peakCgroupMemoryBytes']/MIB,2) if state.get('peakCgroupMemoryBytes') else None,
        'limitations': '趋势阈值用于筛查，不能证明无泄漏。RSS 为 cgroup 匿名内存口径；缓存单列。跳过前 10 分钟，至少两个覆盖 40 分钟的小时窗口才比较增长。未完成运行不能算验收通过。'}


def write_report(directory):
    directory = Path(directory)
    state = json.loads((directory/'state.json').read_text(encoding='utf-8'))
    samples = []
    path = directory/'samples.jsonl'
    if path.exists():
        lines = path.read_text(encoding='utf-8').splitlines(keepends=True)
        samples = [json.loads(line) for line in lines if line.endswith('\n')]
    log = (directory/'load.log').read_text(encoding='utf-8') if (directory/'load.log').exists() else ''
    cycles = [json.loads(m.group(1)) for m in re.finditer(r'SOAK (\{[^\n]+\})', log)]
    cycles = [c for c in cycles if 'heapBytes' in c]
    report = analyze(samples, cycles, state)
    (directory/'resource-report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    with (directory/'resources.csv').open('w', newline='', encoding='utf-8-sig') as file:
        writer = csv.writer(file)
        writer.writerow(['elapsedSeconds','service','memoryMiB','anonymousRSSMiB','cacheMiB','cpuPercentOneCore','pids'])
        previous = {}
        for sample in samples:
            if any(c['Status'] != 'running' for c in sample.get('containers', [])): continue
            at = sample['elapsedSeconds']
            for name, value in sample.get('resources', {}).items():
                old = previous.get(name)
                cpu = round((value['cpuNS']-old[1])/1e9/(at-old[0])*100, 2) if old and at>old[0] else ''
                writer.writerow([at,name,*[round(value[k]/MIB, 2) for k in ['memoryBytes','rssBytes','cacheBytes']],cpu,value['pids']])
                previous[name] = (at,value['cpuNS'])
    rows = ''.join('<tr><td>'+str(h['hour'])+'</td><td><pre>'+html.escape(json.dumps(h,ensure_ascii=False,indent=2))+'</pre></td></tr>' for h in report['hourly'])
    # A bounded SVG preserves spikes via per-minute min/max envelopes.
    series = defaultdict(list)
    for sample in samples:
        if any(c['Status'] != 'running' for c in sample.get('containers', [])): continue
        for name, v in sample.get('resources', {}).items():
            series[name].append((sample['elapsedSeconds'],v['rssBytes']/MIB))
    palette = ['#2563eb','#e87917','#16a34a','#9333ea']
    xmax = max(1, report['observedSeconds'])
    ymax = max([1]+[v for values in series.values() for _,v in values])*1.1
    chart = ''
    legend = ''
    for (name, values), color in zip(sorted(series.items()),palette):
        buckets = defaultdict(list)
        for x,y in values: buckets[int(x//60)].append((x,y))
        points = []
        for group in buckets.values():
            points.extend(sorted([min(group,key=lambda p:p[1]),max(group,key=lambda p:p[1])]))
        line = ' '.join(f'{60+x/xmax*880:.1f},{270-y/ymax*240:.1f}' for x,y in points)
        chart += f'<polyline fill="none" stroke="{color}" stroke-width="1.5" points="{line}"/>'
        legend += f'<span style="color:{color};margin-right:24px">{html.escape(name)}</span>'
    assessment = {'needs_review':'发现增长信号，需要复核','no_growth_flag_observed':'未触发增长筛查阈值，仍需审阅','insufficient_duration':'采样时长不足，尚不能判断趋势'}[report['trendAssessment']]
    summary_rows = ''.join('<tr><td>'+html.escape(name)+'</td>'+''.join('<td>'+str(v[k])+'</td>' for k in ['memoryMedianMiB','memoryPeakMiB','memoryHighWaterMiB','rssMedianMiB','cacheMedianMiB','cpuMeanPercentOneCore','cpuPeakPercentOneCore','pidsPeak'])+'</tr>' for name,v in report['containerSummary'].items())
    content = f'''<!doctype html><meta charset="utf-8"><title>隔离长测资源报告</title>
<style>body{{font:16px/1.6 system-ui;margin:32px auto;max-width:1100px;padding:0 20px;color:#1e293b}}svg{{width:100%;background:#f8fafc}}td,th{{text-align:left;vertical-align:top;border-bottom:1px solid #ddd;padding:12px}}pre{{white-space:pre-wrap;font-size:13px}}.note{{background:#f1f5f9;padding:16px}}</style>
<h1>隔离长测资源报告</h1><p>运行完成：{report['runComplete']}；采样 {report['observedSeconds']/3600:.2f} 小时；完成 {report['cycleCount']} 轮。</p>
<p><b>{assessment}</b></p><p class="note">{html.escape(report['limitations'])}</p>
<p>渲染器进程采样 {report['rendererProcessSamples']} 次；僵尸进程最大值 {report['maxRendererZombies']}；可读文件描述符采样峰值 {report['maxRendererOpenFDs']}（随转换阶段变化，原始数据包含读取失败计数）。</p>
<p>经两秒复查仍存在同一僵尸进程的采样：{report['persistentZombieSamples']} 次。最终空闲检查：</p><pre>{html.escape(json.dumps(report['finalRendererProcesses'],ensure_ascii=False,indent=2))}</pre>
<h2>各服务匿名内存趋势（MiB）</h2><p>{legend}</p>
<svg viewBox="0 0 980 310" role="img" aria-label="服务匿名内存随测试时间变化"><path d="M60 20 V270 H950" stroke="#94a3b8" fill="none"/>{chart}<text x="5" y="30">{ymax:.0f}</text><text x="35" y="270">0</text><text x="60" y="300">0 小时</text><text x="830" y="300">{xmax/3600:.2f} 小时</text></svg>
<h2>需关注的变化</h2><ul>{''.join('<li>'+html.escape(f)+'</li>' for f in report['findings']) or '<li>当前无筛查告警；不等于已证明无泄漏。</li>'}</ul>
<h2>服务资源占用</h2><p>内存单位 MiB；CPU 100% 表示占用一个核心。内存总量包含缓存，PID 数包含线程。合计 cgroup 内存高水位：{report['aggregateHighWaterMiB']} MiB。采样峰值可能漏掉短时尖峰，内核高水位用于补充（旧数据无此指标时回退到采样值）。</p><table><tr><th>服务</th><th>内存中位</th><th>采样峰值</th><th>内核高水位</th><th>匿名内存中位</th><th>缓存中位</th><th>CPU 均值%</th><th>CPU 峰值%</th><th>PID 峰值</th></tr>{summary_rows}</table>
<p>完整 CPU、内存、缓存与 PID 数据：<a href="resources.csv">resources.csv</a>；机器可读结论：<a href="resource-report.json">resource-report.json</a>。</p>
<h2>每小时统计（小时编号从 0 开始）</h2><table><tr><th>小时</th><th>数据</th></tr>{rows}</table>'''
    (directory/'resource-report.html').write_text(content, encoding='utf-8')
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('directory', type=Path)
    args = parser.parse_args()
    result = write_report(args.directory)
    print(json.dumps({k: result[k] for k in ['runComplete','observedSeconds','trendAssessment','findings']},ensure_ascii=False))
