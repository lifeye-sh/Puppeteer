"""Isolated real Compose/systemd upgrade and paired rollback acceptance.

Builds uniquely named local fixture images; never pushes images or publishes
ports. Uses a generated signing key and Go overlay, not the release private key.
Needs the cached Go, beta0.10 component, PG18 and systemd-test images.
"""
from datetime import datetime, timedelta, timezone
import json
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import time


ROOT = Path(__file__).resolve().parents[1]
PROJECT = 'puppeteer-u2-' + secrets.token_hex(4)
WORK = ROOT / 'backend/.local' / PROJECT
WORK.mkdir(parents=True)
RUNNER = PROJECT + '-host'
VOLUME = PROJECT + '-installation'
GO_IMAGE = 'public.ecr.aws/docker/library/golang:1.27.3-alpine'
SERVER_BASE = 'cloners/puppeteer-server:beta0.10'
AUTOMATION = '--automation' in sys.argv


def command(args, *, check=True, data=None, log=None):
    if log:
        with (WORK / log).open('w', encoding='utf-8') as f:
            p = subprocess.run(args, input=data, text=True, encoding='utf-8', stdout=f, stderr=subprocess.STDOUT)
        if check and p.returncode:
            raise RuntimeError('Command failed; protected log: ' + str(WORK / log))
        return ''
    p = subprocess.run(args, input=data, capture_output=True, text=True, encoding='utf-8', errors='replace')
    if check and p.returncode:
        raise RuntimeError(args[0] + ' failed: ' + p.stderr[-1500:])
    return p.stdout.strip()


def docker(*args, **kwargs):
    return command(['docker', *args], **kwargs)


def put(name, contents, destination):
    p = WORK / name
    p.write_text(contents, encoding='utf-8', newline='\n')
    docker('cp', str(p), RUNNER + ':' + destination)


def go(shell, log):
    docker('run', '--rm', '--pull', 'never', '--network', 'none', '-e', 'CGO_ENABLED=0', '-e', 'GOMAXPROCS=2',
           '-v', str(ROOT / 'backend') + ':/src', '-v', 'nt-backend-r01_gomod:/go/pkg/mod',
           '-v', 'nt-backend-r01_gobuild:/root/.cache/go-build', '-w', '/src', GO_IMAGE, 'sh', '-c', shell, log=log)


def pinned(image, repository):
    info = json.loads(docker('image', 'inspect', image))[0]
    return next(v for v in info['RepoDigests'] if v.startswith(repository + '@sha256:'))


def build_fixtures():
    if len(sys.argv) == 3 and sys.argv[1] == '--reuse-binaries':
        previous = Path(sys.argv[2]).resolve()
        require_root = (ROOT / 'backend/.local').resolve()
        if previous.parent != require_root or not previous.name.startswith('puppeteer-u2-'):
            raise RuntimeError('Fixture reuse must reference this repository private artifact directory')
        for name in ('host.test', 'index.json'):
            shutil.copyfile(previous / name, WORK / name)
        refs = {'source': pinned('cloners/puppeteer-server:' + previous.name + '-source', 'cloners/puppeteer-server'),
                'server': pinned('cloners/puppeteer-server:' + previous.name + '-target', 'cloners/puppeteer-server')}
        for image in json.loads(__import__('base64').b64decode(json.loads((WORK / 'index.json').read_text())['payload']))['releases'][0]['images']:
            refs[image['component']] = image['pinnedReference']
        return refs
    relative = '.local/' + PROJECT
    go('go build -o ' + relative + '/signer ./cmd/puppeteer-release-index && ' + relative +
       '/signer init --key ' + relative + '/private.json --public ' + relative + '/public.json', 'key-build.log')
    public = json.loads((WORK / 'public.json').read_text())
    key_id, pub = next(iter(public.items()))
    # Recompile only fixture binaries with this trust root; repository trust is unchanged.
    (WORK / 'trust.go').write_text('package systemupdate\nimport("crypto/ed25519";"encoding/base64")\n'
        'func TrustedKeys()map[string]ed25519.PublicKey{b,_:=base64.StdEncoding.DecodeString(' + json.dumps(pub) +
        ');return map[string]ed25519.PublicKey{' + json.dumps(key_id) + ':b}}\n', encoding='utf-8')
    target_version = 'package buildmeta\nconst Release="beta0.11"\nconst CapabilitiesRevision=90\n'
    (WORK / 'target-version.go').write_text(target_version, encoding='utf-8')
    replacement = {'/src/internal/platform/systemupdate/trust.go': '/src/' + relative + '/trust.go'}
    if AUTOMATION:
        # A build-only overlay supplies the signed fixture catalog to the host
        # scheduler. Production has no configurable update-source environment.
        manager = (ROOT / 'backend/internal/platform/systemupdate/manager.go').read_text(encoding='utf-8')
        manager = manager.replace('func Fetch(ctx context.Context) ([]byte, error) {', 'func Fetch(ctx context.Context) ([]byte, error) {\nif p:=os.Getenv("PUPPETEER_FIXTURE_UPDATE_INDEX");p!=""{return os.ReadFile(p)}')
        (WORK / 'manager.go').write_text(manager, encoding='utf-8')
        replacement['/src/internal/platform/systemupdate/manager.go'] = '/src/' + relative + '/manager.go'
    (WORK / 'source-overlay.json').write_text(json.dumps({'Replace': replacement}))
    (WORK / 'target-overlay.json').write_text(json.dumps({'Replace': dict(replacement, **{
        '/src/internal/platform/buildmeta/version.go': '/src/' + relative + '/target-version.go'})}))
    for variant in ('source', 'target'):
        (WORK / variant).mkdir()
        steps = ['go build -overlay ' + relative + '/' + variant + '-overlay.json -o ' + relative + '/' + variant + '/' +
                 output + ' ./cmd/' + source for output, source in [('server', 'server'), ('nt', 'nt'), ('puppeteer-deployd', 'puppeteer-deployd')]]
        if variant == 'source':
            steps.append('go test -overlay ' + relative + '/source-overlay.json -c -o ' + relative + '/host.test ./internal/platform/hostupgrade')
        go(' && '.join(steps), variant + '-build.log')
        file = WORK / ('Dockerfile.' + variant)
        file.write_text('FROM ' + SERVER_BASE + '\nUSER 0\nCOPY ' + variant + '/ /app/\nRUN chmod 755 /app/server /app/nt /app/puppeteer-deployd\n'
                        'LABEL io.puppeteer.upgrade.hold="v1" io.puppeteer.upgrade.layout="v1"\nUSER 10001:10001\n', encoding='utf-8')
        docker('build', '--network', 'none', '-f', str(file), '-t', 'cloners/puppeteer-server:' + PROJECT + '-' + variant,
               str(WORK), log=variant + '-image.log')
    refs = {'source': pinned('cloners/puppeteer-server:' + PROJECT + '-source', 'cloners/puppeteer-server'),
            'server': pinned('cloners/puppeteer-server:' + PROJECT + '-target', 'cloners/puppeteer-server')}
    for component in ('web', 'graphics', 'renderer'):
        refs[component] = pinned('cloners/puppeteer-' + component + ':beta0.10', 'cloners/puppeteer-' + component)
    now = datetime.now(timezone.utc)
    stamp = lambda value: value.isoformat().replace('+00:00', 'Z')
    release = {'version': 'beta0.11', 'channel': 'beta', 'publishedAt': stamp(now), 'platform': 'linux/amd64',
               'minSource': 'beta0.10', 'schemaVersion': 110, 'capabilitiesRevision': 90,
               'notesUrl': 'https://github.com/lifeye-sh/Puppeteer/releases/tag/beta0.11',
               'images': [{'component': c, 'digest': refs[c].split('@')[1], 'pinnedReference': refs[c]} for c in ('server', 'web', 'graphics', 'renderer')]}
    catalog = {'product': 'Puppeteer', 'format': 1, 'sequence': 1, 'issuedAt': stamp(now),
               'expiresAt': stamp(now + timedelta(hours=6)), 'releases': [release]}
    (WORK / 'catalog.json').write_text(json.dumps(catalog), encoding='utf-8')
    go(relative + '/signer sign --key ' + relative + '/private.json --input ' + relative + '/catalog.json --output ' + relative + '/index.json', 'sign.log')
    return refs


def main():
    refs = build_fixtures()
    base = ''
    installation = ''
    results = []
    tasks_created = []
    try:
        docker('volume', 'create', '--label', 'io.puppeteer.upgrade.fixture=' + PROJECT, VOLUME)
        base = json.loads(docker('volume', 'inspect', VOLUME))[0]['Mountpoint']
        assert base.startswith('/var/lib/docker/volumes/') and base.endswith('/_data')
        installation = base + '/install'
        docker('run', '-d', '--name', RUNNER, '--privileged', '--cgroupns=private', '--tmpfs', '/run', '--tmpfs', '/run/lock',
               '--tmpfs', '/tmp', '-v', VOLUME + ':' + base, '-v', '/var/run/docker.sock:/var/run/docker.sock', 'puppeteer/deployd-systemd-test:local')
        for _ in range(30):
            if docker('exec', RUNNER, 'systemctl', 'is-system-running', check=False) in ('running', 'degraded'):
                break
            time.sleep(1)
        docker('exec', RUNNER, 'mkdir', '-p', installation + '/config/tls', '/fixtures')
        put('docker.service', '[Unit]\nDescription=Isolated fixture Docker transport\n[Service]\nType=oneshot\nExecStart=/usr/bin/docker version --format {{.Server.Version}}\nRemainAfterExit=yes\n', '/etc/systemd/system/docker.service')
        docker('exec', RUNNER, 'systemctl', 'daemon-reload')
        for name in ('enroll-deployd.py', 'enroll-deployd.sh'):
            docker('cp', str(ROOT / 'backend/deploy' / name), RUNNER + ':/fixtures/' + name)
        docker('cp', str(WORK / 'host.test'), RUNNER + ':/fixtures/host.test')
        docker('exec', RUNNER, 'chmod', '755', '/fixtures/host.test')
        setup = r'''import json,os,pathlib,subprocess,sys
root=pathlib.Path(sys.argv[1]);project=sys.argv[2];source=sys.argv[3];web=sys.argv[4]
config=root/'config';secrets=config/'secrets';secrets.mkdir()
values={'database_password':'fixture-only-password','database_url':'postgres://fixture:fixture-only-password@postgres:5432/fixture?sslmode=disable','mfa_key':'2a'*32,'vault_key':'2b'*32}
for name,value in values.items():
 p=secrets/name;p.write_text(value);p.chmod(0o400);os.chown(p,70 if name=='database_password' else 10001,70 if name=='database_password' else 10001)
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','2','-subj','/CN=localhost','-keyout',str(config/'tls/server.key'),'-out',str(config/'tls/server.crt')],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
for p in (config/'tls').iterdir():p.chmod(0o400);os.chown(p,101,101)
(root/'nginx.conf').write_text('pid /tmp/nginx.pid; events {} http { access_log off; client_body_temp_path /tmp/client; proxy_temp_path /tmp/proxy; fastcgi_temp_path /tmp/fastcgi; uwsgi_temp_path /tmp/uwsgi; scgi_temp_path /tmp/scgi; server { listen 8443 ssl; ssl_certificate /run/tls/server.crt; ssl_certificate_key /run/tls/server.key; include /etc/nginx/optional/*.conf; location / { proxy_pass http://server:8089; } } }')
env={'NT_ENV':'production','NT_ADDR':':8089','NT_DATABASE_URL_FILE':'/run/secrets/database_url','NT_DATABASE_NETWORK':'private-container','NT_MFA_KEY_FILE':'/run/secrets/mfa_key','NT_VAULT_KEY_FILE':'/run/secrets/vault_key','NT_ALLOWED_ORIGINS':'https://localhost','NT_BACKUP_DIRECTORY':'/var/lib/puppeteer/backups','NT_RESTORE_DIRECTORY':'/var/lib/puppeteer/restore','NT_MP4_DIRECTORY':'/var/lib/puppeteer/mp4','NT_RUNTIME_PROFILE':'/var/lib/puppeteer/runtime/active.json','NT_STORAGE_PROFILE':'/var/lib/puppeteer/storage/active.json'}
health=lambda test:{'test':test,'interval':'1s','timeout':'3s','retries':30}
model={'services':{'postgres':{'image':'postgres:18-alpine','environment':{'POSTGRES_USER':'fixture','POSTGRES_DB':'fixture','POSTGRES_PASSWORD_FILE':'/run/secrets/database_password'},'secrets':['database_password'],'volumes':['pgdata:/var/lib/postgresql'],'healthcheck':health(['CMD','pg_isready','-U','fixture']), 'restart':'unless-stopped'},'server':{'image':source,'environment':env,'secrets':['database_url','mfa_key','vault_key'],'volumes':['appdata:/var/lib/puppeteer'],'read_only':True,'tmpfs':['/tmp:rw,size=128m,mode=1777'],'healthcheck':health(['CMD','/app/nt','health']),'restart':'unless-stopped','depends_on':{'postgres':{'condition':'service_healthy'}}},'web':{'image':web,'volumes':[str(root/'nginx.conf')+':/etc/nginx/nginx.conf:ro',str(config/'tls')+':/run/tls:ro'],'read_only':True,'tmpfs':['/tmp:rw,size=64m,mode=1777'],'healthcheck':health(['CMD-SHELL','wget -q --no-check-certificate -O /dev/null https://127.0.0.1:8443/healthz']),'restart':'unless-stopped','depends_on':{'server':{'condition':'service_healthy'}}}},'volumes':{'pgdata':{},'appdata':{}},'networks':{'default':{'internal':True}},'secrets':{k:{'file':str(secrets/k)} for k in values}}
for service in model['services'].values():service.update({'mem_limit':'512m','cpus':1.0})
(root/'compose.json').write_text(json.dumps(model));(root/'installed-version').write_text('beta0.10\n')
(config/'production.env').write_text('PUPPETEER_SERVER_IMAGE='+source+'\nPUPPETEER_WEB_IMAGE='+web+'\n')
ctl=root/'puppeteerctl';ctl.write_text('#!/bin/sh\nexec docker compose --project-directory '+str(root)+' -p '+project+' -f '+str(root/'compose.json')+' "$@"\n');ctl.chmod(0o700)
'''
        put('setup.py', setup, '/fixtures/setup.py')
        docker('exec', RUNNER, 'python3', '/fixtures/setup.py', installation, PROJECT, refs['source'], refs['web'])

        def ctl(*args, **kwargs):
            return docker('exec', RUNNER, installation + '/puppeteerctl', *args, **kwargs)

        def sql(query):
            return ctl('exec', '-T', 'postgres', 'psql', '-U', 'fixture', '-d', 'fixture', '-Atc', query)

        ctl('up', '-d', '--no-build', '--pull', 'never', '--wait', '--wait-timeout', '120', log='initial-start.log')
        docker('exec', RUNNER, 'bash', '/fixtures/enroll-deployd.sh', installation, log='enrollment.log')
        # Bootstrap an administrator through the real API, not direct user SQL.
        body = json.dumps({'username': 'upgrade-admin', 'password': 'Isolated-upgrade-test-password',
                           'newPassword2': 'Isolated-upgrade-test-password', 'type': 'admin', 'recording': 'enabled', 'watermark': 'enabled'})
        ctl('exec', '-T', 'server', 'wget', '-q', '-O', '-', '--header=Origin: https://localhost',
            '--header=Content-Type: application/json', '--post-data=' + body, 'http://127.0.0.1:8089/api/setup-user')
        actor = sql("SELECT id FROM users WHERE username='upgrade-admin'")
        assert len(actor) == 36
        sql("CREATE TABLE upgrade_preservation_probe(value text); INSERT INTO upgrade_preservation_probe VALUES('original')")
        ctl('exec', '-T', 'server', 'sh', '-c', "printf original > /var/lib/puppeteer/upgrade-probe")
        docker('cp', str(WORK / 'index.json'), RUNNER + ':' + installation + '/fixture-index.json')
        if AUTOMATION:
            dropin = '/etc/systemd/system/puppeteer-deployd-' + PROJECT + '.service.d'
            docker('exec', RUNNER, 'mkdir', '-p', dropin)
            put('fixture-env.conf', '[Service]\nEnvironment=PUPPETEER_FIXTURE_UPDATE_INDEX=' + installation + '/fixture-index.json\n', dropin + '/fixture.conf')
            docker('exec', RUNNER, 'systemctl', 'daemon-reload')
            docker('exec', RUNNER, 'systemctl', 'restart', 'puppeteer-deployd-' + PROJECT)

        def task_state():
            return json.loads(docker('exec', RUNNER, 'cat', installation + '/config/upgrade-state/upgrade.json'))

        def submit(paused):
            task_id = secrets.token_hex(16)
            tasks_created.append(task_id)
            if not paused:
                # Exercise the real authenticated host HTTP controller, including
                # preview generation, durable acceptance, copied runner and launch.
                script = r'''import http.client,json,pathlib,socket,sys
root=pathlib.Path(sys.argv[1]);actor=sys.argv[2];task=sys.argv[3]
cfg=json.loads((root/'config/deployd.json').read_text());token=pathlib.Path(cfg['tokenFile']).read_text().strip()
def call(path,body=None):
 c=http.client.HTTPConnection('localhost',timeout=60);c.sock=socket.socket(socket.AF_UNIX);c.sock.settimeout(60);c.sock.connect(cfg['socket'])
 c.request('POST' if body is not None else 'GET','/v1/updates/'+path,json.dumps(body) if body is not None else None,{'Authorization':'Bearer '+token,'Content-Type':'application/json'})
 response=c.getresponse();raw=response.read();value=json.loads(raw);c.close()
 if response.status not in (200,202):raise RuntimeError(str(response.status)+' '+str(value.get('error')))
 return value
request={'id':task,'version':'beta0.11','actor':actor,'envelope':json.loads((root/'fixture-index.json').read_text())}
preview=call('preflight',request);assert preview['passed'];request.update({k:preview[k] for k in ('layoutDigest','generation')})
result=call('tasks',request);assert result['task']['id']==task
again=call('tasks',request);assert again['task']['id']==task
print(json.dumps({'id':task,'token':result['task']['progressToken']}))
'''
                if AUTOMATION:
                    script = script[:script.index("request={'id':task")] + r'''
import datetime,time
now=datetime.datetime.now(datetime.timezone.utc)
policy={'revision':call('automation')['policy']['revision'],'mode':'prepare','channel':'beta','maxVersion':'beta0.11','timezone':'UTC','weekdays':list(range(7)),'start':(now-datetime.timedelta(minutes=1)).strftime('%H:%M'),'durationMinutes':120,'expiresAt':(now+datetime.timedelta(hours=3)).isoformat(),'allowSchemaChange':False}
body={'policy':policy,'actor':actor,'confirmedStop':True,'envelope':json.loads((root/'fixture-index.json').read_text())}
call('automation',body)
for _ in range(180):
 state=call('automation');task=call('task')['task']
 if task and task['stage']=='prepared':break
 if state.get('suspended'):raise RuntimeError(state.get('reason'))
 time.sleep(1)
else:raise RuntimeError('prepare scheduler did not finish')
assert task['source']=='beta0.10';print('automatic-prepare-passed',file=sys.stderr)
policy['revision']=state['policy']['revision'];policy['mode']='window';call('automation',body)
previous=task['id']
for _ in range(180):
 task=call('task')['task']
 if task and task['id']!=previous:break
 time.sleep(1)
else:raise RuntimeError('window scheduler did not submit')
print(json.dumps({'id':task['id'],'token':task['progressToken']}))
'''
                result = json.loads(docker('exec', RUNNER, 'python3', '-c', script, installation, actor, task_id))
                task_id = result['id']
                tasks_created.append(task_id)
                assert len(result['token']) == 64
                put('progress-ticket.json', json.dumps(result), '/fixtures/progress-ticket.json')
                return task_id, 'puppeteer-upgrade-' + PROJECT
            env = ['-e', 'PUPPETEER_UPGRADE_FIXTURE_ROOT=' + installation,
                   '-e', 'PUPPETEER_UPGRADE_FIXTURE_ACTION=create', '-e', 'PUPPETEER_UPGRADE_FIXTURE_ID=' + task_id,
                   '-e', 'PUPPETEER_UPGRADE_FIXTURE_ACTOR=' + actor]
            docker('exec', *env, RUNNER, '/fixtures/host.test', '-test.run=^TestHostUpgradeIntegrationHelper$', '-test.v')
            unit = PROJECT + '-upgrade-' + task_id[:8]
            if paused:
                args = ['/fixtures/host.test', '-test.run=^TestHostUpgradeIntegrationHelper$', '-test.timeout=0']
                environment = ['--setenv=PUPPETEER_UPGRADE_FIXTURE_ROOT=' + installation,
                               '--setenv=PUPPETEER_UPGRADE_FIXTURE_PAUSE=true']
            else:
                copy = installation + '/config/upgrade-state/runner-' + task_id
                docker('exec', RUNNER, 'cp', installation + '/config/puppeteer-deployd', copy)
                args = [copy, 'upgrade-run', '--config', installation + '/config/deployd.json']
                environment = []
            docker('exec', RUNNER, 'systemd-run', '--unit=' + unit, '--property=Restart=on-failure', '--property=RestartSec=2',
                   *environment, *args)
            return task_id, unit

        # Exercise interruption first so the fixture still has the source version.
        task_id, unit = submit(True)
        marker = installation + '/config/upgrade-state/verification-paused'
        for _ in range(240):
            state = task_state()
            if state['stage'] == 'verifying' and docker('exec', RUNNER, 'test', '-f', marker, check=False) == '':
                # test's stdout is empty on both success/failure; check explicitly.
                if docker('exec', RUNNER, 'sh', '-c', 'test -f "$1" && echo ready', 'sh', marker, check=False) == 'ready':
                    break
            if state['stage'] in ('failed', 'recovery_required', 'rolled_back'):
                raise RuntimeError('Fixture could not reach verification: ' + state.get('reason', ''))
            time.sleep(1)
        else:
            raise RuntimeError('Timed out waiting for frozen candidate')
        sql("UPDATE upgrade_preservation_probe SET value='modified'; CREATE TABLE failed_migration_probe(value text)")
        ctl('exec', '-T', 'server', 'sh', '-c', 'printf modified > /var/lib/puppeteer/upgrade-probe; touch /var/lib/puppeteer/migration-new-file')
        docker('exec', RUNNER, 'systemctl', 'kill', '--signal=SIGKILL', '--kill-whom=all', unit)
        for _ in range(240):
            state = task_state()
            if state['stage'] in ('rolled_back', 'recovery_required', 'failed'):
                break
            time.sleep(1)
        assert state['stage'] == 'rolled_back', state.get('reason')
        assert sql('SELECT value FROM upgrade_preservation_probe') == 'original'
        assert sql("SELECT to_regclass('failed_migration_probe') IS NULL") == 't'
        assert ctl('exec', '-T', 'server', 'cat', '/var/lib/puppeteer/upgrade-probe') == 'original'
        ctl('exec', '-T', 'server', 'test', '!', '-e', '/var/lib/puppeteer/migration-new-file')
        results.append({'scenario': 'killed-runner-after-switch', 'state': 'rolled_back', 'databaseAndFilesRestored': True})
        print(json.dumps(results[-1]), flush=True)

        task_id, unit = submit(False)
        progress_observed = False
        web_progress_observed = False
        for _ in range(240):
            state = task_state()
            if state['stage'] in ('succeeded', 'rolled_back', 'recovery_required', 'failed'):
                break
            if state['stage'] in ('preparing', 'quiescing', 'backing_up', 'switching', 'verifying'):
                progress_script = r'''import http.client,json,pathlib,socket,sys
root=pathlib.Path(sys.argv[1]);ticket=json.loads(pathlib.Path('/fixtures/progress-ticket.json').read_text())
c=http.client.HTTPConnection('localhost',timeout=5);c.sock=socket.socket(socket.AF_UNIX);c.sock.settimeout(5);c.sock.connect(str(root/'config/deployd-run/upgrade.sock'))
c.request('GET','/upgrade-progress/'+ticket['id']+'?ticket='+ticket['token']);r=c.getresponse();v=json.loads(r.read());assert r.status==200 and v['id']==ticket['id'];assert 'plan' not in v and 'progressToken' not in v
print('progress-ok')
'''
                # Socket may not exist during the first launch instant; validate
                # once the runner is executing rather than assuming API progress.
                if docker('exec', RUNNER, 'python3', '-c', progress_script, installation, check=False) == 'progress-ok':
                    progress_observed = True
                    if state['stage'] == 'backing_up':
                        # Query through the real Nginx route while server is
                        # stopped, with no application cookie or bearer token.
                        web_script = r'''import json,pathlib,subprocess,sys
root=pathlib.Path(sys.argv[1]);ticket=json.loads(pathlib.Path('/fixtures/progress-ticket.json').read_text())
url='https://127.0.0.1:8443/upgrade-progress/'+ticket['id']+'?ticket='+ticket['token']
p=subprocess.run([str(root/'puppeteerctl'),'exec','-T','web','wget','-q','--no-check-certificate','-O','-',url],capture_output=True)
assert p.returncode==0;v=json.loads(p.stdout);assert v['id']==ticket['id'];print('web-progress-ok')
'''
                        if docker('exec', RUNNER, 'python3', '-c', web_script, installation, check=False) == 'web-progress-ok':
                            web_progress_observed = True
            time.sleep(1)
        assert state['stage'] == 'succeeded', state.get('reason')
        assert sql('SELECT value FROM upgrade_preservation_probe') == 'original'
        ready = json.loads(ctl('exec', '-T', 'server', '/app/nt', 'upgrade-control', 'ready', actor))
        assert ready['version'] == 'beta0.11' and ready['frozen'] is False
        assert docker('exec', RUNNER, 'systemctl', 'is-active', 'puppeteer-deployd-' + PROJECT + '.service') == 'active'
        assert progress_observed, 'Independent progress endpoint was not observed'
        assert web_progress_observed, 'Nginx progress during backend shutdown was not observed'
        results.append({'scenario': 'automatic-prepare-and-window' if AUTOMATION else 'independent-production-runner', 'state': 'succeeded', 'version': ready['version'], 'executorReplaced': True, 'controllerAndProgress': True, 'nginxProgressWithoutBackend': True})
        (WORK / 'acceptance.json').write_text(json.dumps(results, indent=2), encoding='utf-8')
        print(json.dumps(results[-1]), flush=True)
    finally:
        if installation:
            docker('exec', RUNNER, installation + '/puppeteerctl', 'logs', '--tail', '60', check=False, log='services.log')
            docker('exec', RUNNER, 'journalctl', '--no-pager', '-n', '160', check=False, log='host-journal.log')
            docker('cp', RUNNER + ':' + installation + '/config/upgrade-state', str(WORK / 'host-state'), check=False)
            docker('exec', RUNNER, 'systemctl', 'stop', '--no-block', PROJECT + '-upgrade-*', check=False)
            docker('exec', RUNNER, 'systemctl', 'kill', '--signal=SIGKILL', '--kill-whom=all', PROJECT + '-upgrade-*', check=False)
            docker('exec', RUNNER, 'systemctl', 'stop', '--no-block', 'puppeteer-upgrade-' + PROJECT, check=False)
            docker('exec', RUNNER, 'systemctl', 'kill', '--signal=SIGKILL', '--kill-whom=all', 'puppeteer-upgrade-' + PROJECT, check=False)
            for task_id in tasks_created:
                docker('rm', '-f', PROJECT + '-upgrade-' + task_id[:16], check=False)
            docker('exec', RUNNER, 'systemctl', 'stop', 'puppeteer-deployd-' + PROJECT + '.service', check=False)
            docker('exec', RUNNER, installation + '/puppeteerctl', 'down', '--volumes', '--remove-orphans', check=False)
        docker('rm', '-f', '-v', RUNNER, check=False)
        docker('volume', 'rm', VOLUME, check=False)
        # Images are retained as unique local test artifacts, never pushed.


if __name__ == '__main__':
    main()
