"""Fixed host upgrade operations, embedded in the immutable Go runner.

Input is supplied by the trusted runner, never a browser command or URL.
Private recipes/archives are kept outside application volumes. No subprocess
output or resolved environment is written to public progress/error messages.
"""
import hashlib
from datetime import datetime, timezone
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
from urllib.parse import urlsplit


class Rejected(Exception):
    pass


def require(ok, reason):
    if not ok:
        raise Rejected(reason)


def digest(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for part in iter(lambda: f.read(1024 * 1024), b''):
            h.update(part)
    return h.hexdigest()


def sync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def atomic(path, raw, mode=0o600):
    path = Path(path)
    fd, name = tempfile.mkstemp(prefix='.upgrade-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as f:
            os.fchmod(f.fileno(), mode)
            f.write(raw)
            f.flush()
            os.fsync(f.fileno())
        os.replace(name, path)
        sync_dir(path.parent)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def json_write(path, value):
    atomic(path, json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode())


def safe_archive(path):
    # Reject links/devices rather than allowing an archive to escape a volume.
    with tarfile.open(path, 'r|') as archive:
        for item in archive:
            p = PurePosixPath(item.name)
            require(not p.is_absolute() and '..' not in p.parts and (item.isfile() or item.isdir()),
                    '备份含不支持的链接、设备或目录路径')


def check_component_state(service, current):
    disabled = any(p.startswith('puppeteer-') and p.endswith('-disabled') for p in service.get('profiles', []))
    # Stopped disabled containers may retain an earlier release's image.
    if disabled:
        require(not current['State']['Running'], '配置停用的组件仍在运行，请先核对组件状态')
    else:
        require(current['Config']['Image'] == service['image'], '实际容器镜像与部署配置不同')


class Operations:
    def __init__(self, request):
        self.plan = request['plan']
        self.release = request.get('release')
        self.executor = request['executor']
        self.root = Path(self.executor['Directory'])
        self.project = self.executor['Project']
        self.docker = self.executor['Docker']
        require(self.root.is_absolute() and self.root != Path('/') and self.root.resolve() == self.root,
                '安装目录必须是实际绝对路径')
        require(re.fullmatch(r'[a-z][a-z0-9-]{2,40}', self.project), '部署项目名称无效')
        require(re.fullmatch(r'[a-f0-9]{32}', self.plan['id']), '升级任务标识无效')
        self.compose = self.root / 'compose-managed.json'
        require(self.executor['Files'] == [str(self.compose)] and not self.executor.get('EnvFile'),
                '仅支持单一受管 Compose 配置')
        require(Path(self.docker).is_absolute(), 'Docker 路径无效')
        self.state = self.root / 'config/upgrade-state'
        self.job = self.state / self.plan['id']
        self.job.mkdir(mode=0o700, parents=True, exist_ok=True)
        require(self.job.resolve() == self.job, '升级状态目录不能是符号链接')
        self.hold = self.root / 'config/deployd-run/upgrade-hold'
        self.unit = 'puppeteer-deployd-' + self.project + '.service'
        self.binary = self.root / 'config/puppeteer-deployd'
        self.recipe_path = self.job / 'recipe.json'
        self.helper_name = self.project + '-upgrade-' + self.plan['id'][:16]
        self.pg_application = 'puppeteer-upgrade-' + self.plan['id']

    def command(self, args, *, stdin=None, output=None, timeout=300, reason='宿主机操作失败'):
        # Arguments are arrays; shell expansion is never used for host commands.
        try:
            p = subprocess.run(args, stdin=stdin, stdout=output or subprocess.PIPE,
                               stderr=subprocess.DEVNULL, timeout=timeout, cwd=self.root)
        except (subprocess.SubprocessError, OSError):
            raise Rejected(reason) from None
        require(p.returncode == 0, reason)
        raw = p.stdout if output is None else b''
        require(len(raw) <= 8 * 1024 * 1024, '宿主机响应过大')
        return raw

    def docker_call(self, *args, **kwargs):
        return self.command([self.docker, *args], **kwargs)

    def ctl(self, *args, file=None, **kwargs):
        if args[:3] == ('exec', '-T', 'postgres'):
            args = ('exec', '-T', '-e', 'PGAPPNAME=' + self.pg_application, 'postgres', *args[3:])
        return self.docker_call('compose', '--project-directory', str(self.root), '--project-name', self.project,
                                '-f', str(file or self.compose), *args, **kwargs)

    def inspect(self, kind, identity):
        raw = self.docker_call(kind, 'inspect', identity, reason='无法核验部署对象')
        return json.loads(raw)[0]

    def recipe(self):
        value = json.loads(self.recipe_path.read_text())
        require(value['id'] == self.plan['id'] and value['layoutDigest'] == self.plan['layoutDigest'],
                '升级恢复配方与任务不匹配')
        return value

    def control(self, operation):
        raw = self.ctl('exec', '-T', 'server', '/app/nt', 'upgrade-control', operation, self.plan['actor'],
                       timeout=55, reason='应用升级检查或排空失败')
        value = json.loads(raw)
        require(value.get('passed') is True or (operation == 'ready' and value.get('status') == 'ready'),
                '应用仍有维护或执行任务，或操作者权限已变化')
        return value

    def original_layout(self):
        require(not self.compose.is_symlink() and digest(self.compose) == self.plan['layoutDigest'],
                '部署配置已变化，请重新预检')

    def volume(self, name):
        v = self.inspect('volume', name)
        require(v.get('Driver') == 'local' and not v.get('Options') and
                (v.get('Labels') or {}).get('com.docker.compose.project') == self.project,
                '存在外部或未受管数据卷')
        users = self.docker_call('ps', '-aq', '--filter', 'volume=' + name).decode().split()
        for container in users:
            item = self.inspect('container', container)
            labels = item.get('Config', {}).get('Labels') or {}
            require(labels.get('com.docker.compose.project') == self.project and
                    labels.get('com.docker.compose.service') in ('server', 'guacd', 'mp4-renderer', 'postgres'),
                    '数据卷被其他容器引用，不能自动恢复')

    def volume_command(self, recipe, name, args, *, writable=False, stdin=None, output=None):
        self.volume(name)
        mount = 'type=volume,src=' + name + ',dst=/data' + ('' if writable else ',readonly')
        return self.docker_call('run', '-i', '--rm', '--name', self.helper_name,
                               '--label', 'io.puppeteer.upgrade.job=' + self.plan['id'],
                               '--network', 'none', '--read-only', '--user', '0',
                               '--security-opt', 'no-new-privileges', '--mount', mount,
                               '--entrypoint', args[0], recipe['oldServerImage'], *args[1:],
                               stdin=stdin, output=output, timeout=1800, reason='数据卷备份或恢复失败')

    def cleanup_helpers(self):
        ids = self.docker_call('ps', '-aq', '--filter', 'name=^/' + self.helper_name + '$').decode().split()
        for identity in ids:
            info = self.inspect('container', identity)
            require((info['Config'].get('Labels') or {}).get('io.puppeteer.upgrade.job') == self.plan['id'],
                    '升级辅助容器名称被其他容器占用')
            self.docker_call('rm', '-f', identity, reason='无法停止中断的升级辅助容器')
        if self.recipe_path.exists():
            recipe = self.recipe()
            query = "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name='" + self.pg_application + "' AND pid<>pg_backend_pid()"
            self.ctl('exec', '-T', 'postgres', 'psql', '-U', recipe['user'], '-d', 'postgres', '-Atc', query,
                     reason='无法停止中断的升级数据库操作')

    def check_space(self, recipe):
        size = int(self.ctl('exec', '-T', 'postgres', 'psql', '-U', recipe['user'], '-d', recipe['database'],
                            '-Atc', 'SELECT pg_database_size(current_database())').strip())
        for volume in recipe['volumes']:
            amount = self.volume_command(recipe, volume, ['du', '-sk', '/data'])
            size += int(amount.split()[0]) * 1024
        for directory, children, files in os.walk(self.root, followlinks=False):
            base = Path(directory)
            children[:] = [name for name in children if not name.startswith('.upgrade-') and
                           base / name not in (self.root / 'backups', self.state, self.hold.parent)]
            for name in files:
                path = base / name
                if path.name == '.upgrade.lock':
                    continue
                require(not path.is_symlink(), '配置目录含符号链接，需要专用备份适配')
                size += path.stat().st_size
        budget = size * 2 + 1024 * 1024 * 1024
        require(shutil.disk_usage(self.job).free >= budget, '升级备份空间不足，需要至少两倍当前数据大小及 1 GiB 余量')
        return budget

    def prepare(self):
        self.check_automation()
        self.cleanup_helpers()
        self.original_layout()
        require(self.release and self.release['version'] == self.plan['target'], '缺少可信目标版本')
        require(Path('/proc/1/comm').read_text().strip() == 'systemd' and Path('/run/systemd/system').is_dir(),
                '宿主机未运行 systemd')
        self.command(['systemctl', 'is-active', '--quiet', self.unit], reason='部署执行器服务未就绪')
        source = self.control('check')
        require(source['version'] == self.plan['source'] and not source['frozen'], '运行版本已变化或仍处于维护状态')
        raw_model = json.loads(self.compose.read_text())
        model = json.loads(self.ctl('config', '--format', 'json'))
        services = model['services']
        require(set(services) <= {'postgres', 'server', 'web', 'guacd', 'mp4-renderer'} and
                {'postgres', 'server', 'web'} <= set(services), '包含未支持的服务或缺少核心服务')
        require(not model.get('x-puppeteer-wal'), 'WAL 专用部署需要独立升级适配')
        env = services['server'].get('environment', {})
        require(env.get('NT_UPGRADE_HOLD') == '/run/puppeteer-deploy/upgrade-hold', '当前版本尚未登记升级冻结配置')
        require(env.get('NT_RUNTIME_PROFILE') == '/var/lib/puppeteer/runtime/active.json', '不支持自定义运行时目录')
        require(env.get('NT_RDP_DRIVE_ENABLED', 'false') == 'false', '启用共享盘的部署尚需专用升级适配')
        # A restored database may not be the Compose bootstrap database.
        profile = self.ctl('exec', '-T', 'server', 'sh', '-c', 'if [ -e /var/lib/puppeteer/runtime/active.json ]; then echo present; else echo absent; fi')
        require(profile.strip() == b'absent', '当前实例已切换恢复数据库，暂不支持自动升级')
        pg = services['postgres']
        require(str(pg.get('image', '')).startswith('postgres:18-') and not pg.get('command') and not pg.get('ports'),
                '仅支持标准内部 PostgreSQL 18，不能自动跨数据库大版本升级')
        user, database = pg.get('environment', {}).get('POSTGRES_USER'), pg.get('environment', {}).get('POSTGRES_DB')
        require(all(isinstance(v, str) and re.fullmatch('[a-z][a-z0-9_]{0,62}', v) for v in (user, database)),
                '数据库标识不符合受管布局')
        require(database not in ('postgres', 'template0', 'template1'), '不能对维护数据库执行自动恢复')
        dburl = env.get('NT_DATABASE_URL', '')
        if not dburl and env.get('NT_DATABASE_URL_FILE') == '/run/secrets/database_url':
            dburl = self.ctl('exec', '-T', 'server', 'cat', '/run/secrets/database_url').decode().strip()
        parsed = urlsplit(dburl)
        require(parsed.scheme in ('postgres', 'postgresql') and parsed.hostname == 'postgres' and
                parsed.port in (None, 5432) and parsed.username == user and parsed.path == '/' + database,
                '应用数据库与受管备份数据库不一致')
        recipe = {'id': self.plan['id'], 'layoutDigest': self.plan['layoutDigest'], 'user': user, 'database': database,
                  'running': [], 'volumes': [], 'oldImages': {}, 'targetImages': {}, 'targetIDs': {}}
        hold_mount = False
        for name, service in services.items():
            require(not service.get('network_mode') and not service.get('privileged') and not service.get('devices'),
                    '不支持主机网络、特权或设备映射服务')
            for mount in service.get('volumes', []):
                if mount['type'] == 'bind':
                    path = Path(mount['source'])
                    require(path.resolve().is_relative_to(self.root) and mount.get('read_only') is True,
                            '存在安装目录外的挂载或可写宿主机目录')
                    if name == 'server' and mount['target'] == '/run/puppeteer-deploy':
                        hold_mount = path == self.hold.parent
                elif mount['type'] == 'volume':
                    spec = model['volumes'][mount['source']]
                    volume = spec['name']
                    self.volume(volume)
                    if name != 'postgres' and not mount.get('read_only') and volume not in recipe['volumes']:
                        require(mount['target'] in ('/var/lib/puppeteer', '/var/lib/puppeteer/storage'), '存在未知可写数据卷')
                        recipe['volumes'].append(volume)
                else:
                    raise Rejected('存在无法备份的挂载类型')
            ids = self.ctl('ps', '-aq', name).decode().split()
            require(len(ids) <= 1, '不支持多副本部署')
            if ids:
                current = self.inspect('container', ids[0])
                check_component_state(service, current)
                recipe['oldImages'][name] = current['Image']
                if current['State']['Running']:
                    require(current['State'].get('Health', {}).get('Status') == 'healthy', '当前组件尚不健康')
                    recipe['running'].append(name)
        require(hold_mount and {'server', 'web', 'postgres'} <= set(recipe['running']) and recipe['volumes'],
                '缺少冻结挂载、运行中的核心组件或应用数据卷')
        recipe['oldServerImage'] = recipe['oldImages']['server']
        recipe['sourceSchema'] = int(self.ctl('exec', '-T', 'postgres', 'psql', '-U', user, '-d', database,
                                              '-Atc', 'SELECT schema_version FROM instance_state').strip())
        require(self.release['schemaVersion'] >= recipe['sourceSchema'], '目标数据库版本低于当前数据库')
        if self.plan.get('automationRevision') and not self.plan.get('allowSchemaChange'):
            require(self.release['schemaVersion'] == recipe['sourceSchema'], '自动升级授权不允许数据库结构版本变化')
        self.check_space(recipe)
        for image in self.release['images']:
            ref, component = image['pinnedReference'], image['component']
            require(re.fullmatch('cloners/puppeteer-' + component + '@sha256:[a-f0-9]{64}', ref), '目标镜像引用无效')
            # Already present immutable references do not need another network pull.
            try:
                target = self.inspect('image', ref)
            except Rejected:
                self.docker_call('pull', ref, timeout=1200, reason='目标镜像下载失败')
                target = self.inspect('image', ref)
            require(target['Os'] == 'linux' and target['Architecture'] == 'amd64', '目标镜像平台不兼容')
            if component == 'server':
                labels = target.get('Config', {}).get('Labels') or {}
                require(labels.get('io.puppeteer.upgrade.hold') == 'v1' and labels.get('io.puppeteer.upgrade.layout') == 'v1',
                        '目标版本缺少冻结能力或不兼容当前部署格式')
            recipe['targetImages'][component] = ref
            recipe['targetIDs'][component] = target['Id']
        self.check_space(recipe)
        candidate = json.loads(json.dumps(raw_model))
        mapping = {'server': 'server', 'web': 'web', 'guacd': 'graphics', 'mp4-renderer': 'renderer'}
        for service, component in mapping.items():
            if service in candidate['services']:
                require(re.fullmatch('cloners/puppeteer-' + component + '@sha256:[a-f0-9]{64}', str(services[service]['image'])),
                        '原组件必须固定官方镜像摘要，不能自动替换标签或自定义镜像')
                candidate['services'][service]['image'] = recipe['targetImages'][component]
        drive = candidate.get('x-puppeteer-drive', {}).get('recipe')
        if drive:
            drive['image'] = recipe['targetImages']['graphics']
        json_write(self.job / 'candidate.json', candidate)
        recipe['candidateDigest'] = digest(self.job / 'candidate.json')
        self.ctl('config', '--quiet', file=self.job / 'candidate.json')
        old = json.loads(json.dumps(raw_model))
        json_write(self.job / 'previous.json', old)
        for source, dest in ((self.compose, 'compose.original'), (self.root / 'config/production.env', 'env.original'),
                             (self.binary, 'executor.original')):
            require(source.is_file() and not source.is_symlink(), '缺少原部署配置或执行器')
            atomic(self.job / dest, source.read_bytes())
        container = self.docker_call('create', recipe['targetImages']['server']).decode().strip()
        try:
            self.docker_call('cp', container + ':/app/puppeteer-deployd', str(self.job / 'executor.next'))
        finally:
            self.docker_call('rm', container)
        os.chmod(self.job / 'executor.next', 0o700)
        self.command([str(self.job / 'executor.next'), '--help'], timeout=10, reason='目标部署执行器不可执行')
        recipe['executorNextDigest'] = digest(self.job / 'executor.next')
        self.original_layout()
        json_write(self.recipe_path, recipe)

    def quiesce(self):
        recipe = self.recipe()
        self.original_layout()
        self.control('check')
        self.check_automation()
        atomic(self.job / 'quiesce-intent', b'1')
        atomic(self.hold, self.plan['id'].encode(), 0o644)
        self.control('drain')
        self.stop(recipe)
        self.check_space(recipe)

    def check_automation(self):
        revision = self.plan.get('automationRevision', 0)
        if not revision:
            return
        state = json.loads((self.state / 'automation.json').read_text())
        policy = state['policy']
        require(policy['revision'] == revision and policy['mode'] == ('prepare' if self.plan.get('prepareOnly') else 'window')
                and state['actor'] == self.plan['actor'] and not state.get('suspended'), '自动升级授权已变化或暂停')
        now = datetime.now(timezone.utc)
        require(now < datetime.fromisoformat(policy['expiresAt'].replace('Z', '+00:00')) and
                now < datetime.fromisoformat(self.plan['startBefore'].replace('Z', '+00:00')), '维护窗口或自动升级授权已到期，未开始停机')

    def stop(self, recipe):
        # The read-only web container keeps the independent progress endpoint
        # available while every application-data writer is stopped.
        apps = [s for s in recipe['running'] if s not in ('postgres', 'web')]
        self.ctl('stop', '--timeout', '30', *apps, timeout=180, reason='无法停止全部应用写入者')
        for service in apps:
            for identity in self.ctl('ps', '-aq', service).decode().split():
                require(not self.inspect('container', identity)['State']['Running'], '应用写入者仍在运行')

    def stream(self, name, produce):
        path = self.job / name
        part = self.job / (name + '.partial')
        with open(part, 'wb') as f:
            os.fchmod(f.fileno(), 0o600)
            produce(f)
            f.flush()
            os.fsync(f.fileno())
        os.replace(part, path)
        sync_dir(self.job)

    def backup(self):
        recipe = self.recipe()
        self.stop(recipe)
        self.stream('database.dump', lambda out: self.ctl('exec', '-T', 'postgres', 'pg_dump', '-U', recipe['user'],
                    '-d', recipe['database'], '-Fc', output=out, timeout=1800, reason='数据库备份失败'))
        for index, volume in enumerate(recipe['volumes']):
            self.stream('volume-' + str(index) + '.tar', lambda out: self.volume_command(recipe, volume, ['tar', '-C', '/data', '-cpf', '-', '.'], output=out))
        self.stream('configuration.tar', lambda out: self.command(['tar', '--exclude=./backups', '--exclude=./.upgrade-*',
                    '--exclude=./.upgrade.lock', '--exclude=./config/upgrade-state', '--exclude=./config/deployd-run',
                    '-C', str(self.root), '-cpf', '-', '.'], output=out, timeout=1800, reason='配置备份失败'))
        files = ['database.dump', 'configuration.tar', 'compose.original', 'env.original', 'executor.original', 'previous.json', 'recipe.json']
        files += ['volume-' + str(i) + '.tar' for i in range(len(recipe['volumes']))]
        json_write(self.job / 'checksums.json', {name: digest(self.job / name) for name in files})

    def validate(self):
        recipe = self.recipe()
        checksums = json.loads((self.job / 'checksums.json').read_text())
        expected = {'database.dump', 'configuration.tar', 'compose.original', 'env.original', 'executor.original', 'previous.json', 'recipe.json'}
        expected.update('volume-' + str(i) + '.tar' for i in range(len(recipe['volumes'])))
        require(set(checksums) == expected, '备份清单缺项或包含未知内容')
        for name, value in checksums.items():
            require(digest(self.job / name) == value, '备份摘要校验失败')
            if name.endswith('.tar'):
                safe_archive(self.job / name)
        with open(self.job / 'database.dump', 'rb') as f:
            self.ctl('exec', '-T', 'postgres', 'pg_restore', '--list', stdin=f, reason='数据库备份不可读取')

    def switch(self):
        recipe = self.recipe()
        require(self.hold.is_file() and digest(self.job / 'executor.next') == recipe['executorNextDigest'] and
                digest(self.job / 'candidate.json') == recipe['candidateDigest'], '冻结标记、候选配置或目标执行器发生变化')
        atomic(self.compose, (self.job / 'candidate.json').read_bytes())
        env = (self.job / 'env.original').read_text()
        for component, ref in recipe['targetImages'].items():
            key = 'PUPPETEER_' + component.upper() + '_IMAGE'
            pattern = r'^' + key + '=.*$'
            env = re.sub(pattern, key + '=' + ref, env, flags=re.M) if re.search(pattern, env, re.M) else env + '\n' + key + '=' + ref + '\n'
        atomic(self.root / 'config/production.env', env.encode())
        self.command(['systemctl', 'stop', self.unit], timeout=660, reason='无法停止旧部署执行器')
        atomic(self.binary, (self.job / 'executor.next').read_bytes(), 0o700)
        self.command(['systemctl', 'start', self.unit], reason='新部署执行器启动失败')
        self.start(recipe)

    def start(self, recipe):
        apps = [s for s in recipe['running'] if s != 'postgres']
        self.ctl('up', '-d', '--no-build', '--no-deps', '--pull', 'never', '--force-recreate', '--wait', '--wait-timeout', '240',
                 *apps, timeout=300, reason='应用组件启动或健康检查失败')

    def verify(self):
        recipe = self.recipe()
        self.command(['systemctl', 'is-active', '--quiet', self.unit], reason='新部署执行器未就绪')
        require(digest(self.binary) == recipe['executorNextDigest'], '执行器版本文件不匹配')
        mapping = {'server': 'server', 'web': 'web', 'guacd': 'graphics', 'mp4-renderer': 'renderer'}
        for service in recipe['running']:
            ids = self.ctl('ps', '-q', service).decode().split()
            require(len(ids) == 1, '升级后的组件副本数不匹配')
            item = self.inspect('container', ids[0])
            require(item['State'].get('Health', {}).get('Status') == 'healthy', '升级后的组件不健康')
            if service in mapping:
                require(item['Image'] == recipe['targetIDs'][mapping[service]], '运行镜像摘要不匹配')
        ready = self.control('ready')
        require(ready['version'] == self.plan['target'] and ready['frozen'] and ready['upgradeHold'], '新版本未保持冻结或版本不匹配')
        schema = self.ctl('exec', '-T', 'postgres', 'psql', '-U', recipe['user'], '-d', recipe['database'],
                          '-Atc', 'SELECT schema_version FROM instance_state').decode().strip()
        require(int(schema) == self.release['schemaVersion'], '迁移后的数据库版本不匹配')
        self.ctl('exec', '-T', 'web', 'nginx', '-t', reason='新版本 Nginx 配置验证失败')
        self.ctl('exec', '-T', 'web', 'wget', '-q', '--no-check-certificate', '-O', '/dev/null', 'https://127.0.0.1:8443/healthz', reason='网页管理入口未就绪')

    def activate(self):
        atomic(self.root / 'installed-version', (self.plan['target'] + '\n').encode())
        self.open_writes(self.plan['target'])

    def open_writes(self, version):
        if self.hold.exists():
            require(self.hold.read_text() == self.plan['id'], '冻结标记不属于当前任务')
            self.hold.unlink()
            sync_dir(self.hold.parent)
        for _ in range(60):
            value = self.control('ready')
            if value['version'] == version and value['frozen'] is False:
                return
            time.sleep(1)
        raise Rejected('业务仍处于冻结状态，保留激活任务等待核验')

    def fence(self):
        atomic(self.hold, self.plan['id'].encode(), 0o644)
        self.cleanup_helpers()
        self.stop(self.recipe())
        self.command(['systemctl', 'stop', self.unit], timeout=660, reason='恢复前无法停止执行器')

    def restore(self):
        self.cleanup_helpers()
        recipe = self.recipe()
        self.validate()
        self.stop(recipe)
        # All writers are stopped. Recreate the exact managed database to remove
        # objects added by a failed migration; never replay an old dump in-place.
        for args in [('dropdb', '--if-exists', '--force', '-U', recipe['user'], recipe['database']),
                     ('createdb', '-U', recipe['user'], '-O', recipe['user'], recipe['database'])]:
            self.ctl('exec', '-T', 'postgres', *args, reason='恢复目标数据库准备失败')
        with open(self.job / 'database.dump', 'rb') as f:
            self.ctl('exec', '-T', 'postgres', 'pg_restore', '--exit-on-error', '-U', recipe['user'], '-d', recipe['database'],
                     stdin=f, timeout=1800, reason='数据库恢复失败')
        for index, volume in enumerate(recipe['volumes']):
            # The only deletion target is the prevalidated named-volume mount.
            # dotfiles are included; find does not follow symlinks.
            self.volume_command(recipe, volume, ['find', '/data', '-mindepth', '1', '-maxdepth', '1', '-exec', 'rm', '-rf', '--', '{}', ';'], writable=True)
            with open(self.job / ('volume-' + str(index) + '.tar'), 'rb') as f:
                self.volume_command(recipe, volume, ['tar', '-C', '/data', '-xpf', '-'], writable=True, stdin=f)
        atomic(self.compose, (self.job / 'previous.json').read_bytes())
        atomic(self.root / 'config/production.env', (self.job / 'env.original').read_bytes())
        atomic(self.binary, (self.job / 'executor.original').read_bytes(), 0o700)

    def resume(self):
        if not (self.job / 'quiesce-intent').exists():
            return
        recipe = self.recipe()
        self.cleanup_helpers()
        atomic(self.hold, self.plan['id'].encode(), 0o644)
        self.command(['systemctl', 'start', self.unit], reason='原部署执行器无法启动')
        self.start(recipe)
        value = self.control('ready')
        require(value['version'] == self.plan['source'] and value['frozen'], '原部署未在冻结状态恢复')
        schema = self.ctl('exec', '-T', 'postgres', 'psql', '-U', recipe['user'], '-d', recipe['database'],
                          '-Atc', 'SELECT schema_version FROM instance_state').strip()
        require(int(schema) == recipe['sourceSchema'], '恢复后的数据库版本不匹配')
        self.open_writes(self.plan['source'])


def main():
    os.umask(0o077)
    try:
        require(sys.version_info >= (3, 9), '宿主机需要 Python 3.9 或更高版本')
        request = json.loads(sys.stdin.buffer.read(2 * 1024 * 1024))
        operation = request['operation']
        require(operation in ('prepare', 'quiesce', 'backup', 'validate', 'switch', 'verify', 'activate', 'fence', 'restore', 'resume'), '未知升级操作')
        getattr(Operations(request), operation)()
        print(json.dumps({'passed': True}))
    except Rejected as e:
        print(json.dumps({'passed': False, 'reason': str(e)}, ensure_ascii=False))
        sys.exit(1)
    except Exception:
        # Unexpected exceptions can contain URLs, credentials or resolved config.
        print(json.dumps({'passed': False, 'reason': '宿主机升级操作异常，请检查受保护的任务记录'}, ensure_ascii=False))
        sys.exit(1)


if __name__ == '__main__':
    main()
