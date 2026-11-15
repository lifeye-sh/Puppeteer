"""Opt-in real Docker smoke test. Creates only a uniquely named disposable project.
Requires local puppeteer/server:p0-integration and puppeteer/installer-test:local.
Run from the repository host; does not require a Docker socket in application containers.
"""
import argparse
import importlib.util
import json
import secrets
import subprocess
import time
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--mp4", action="store_true", help="exercise internal renderer, sample conversion, queue preservation and recovery")
parser.add_argument("--drive", action="store_true", help="exercise shared-drive FUSE deployment and removal")
parser.add_argument("--graphics", action="store_true", help="exercise internal graphics lifecycle instead of protocol ports")
parser.add_argument("--vnc-fixture", type=Path, help="compiled Linux backend/fixtures/vnc binary for a representative graphics session")
parser.add_argument("--server-image", default="puppeteer/server:p0-integration")
parser.add_argument("--web-image", default="nginx:alpine", help="web runtime to validate, including a release image")
parser.add_argument("--executor", type=Path, help="explicit freshly built Linux executor override")
parser.add_argument("--recovery", action="store_true", help="inject daemon crashes and verify automatic reconciliation")
parser.add_argument("--recovery-only", action="store_true")
parser.add_argument("--systemd", action="store_true", help="use actual systemd and packaged enrollment in a privileged disposable runner")
parser.add_argument("--systemd-smoke", action="store_true", help="short enrollment/progress regression; requires --systemd")
arguments = parser.parse_args()
if arguments.drive: arguments.graphics=True
if arguments.systemd and (arguments.recovery or arguments.recovery_only):
    parser.error("systemd enrollment and crash-injection runs are separate scenarios")
ROOT = Path(__file__).resolve().parents[1]
project = "puppeteer-p0-" + secrets.token_hex(5)
runner = project + "-runner"
volume = project + "-config"
extract = project + "-extract"
artifacts = ROOT / "backend/.local" / project
artifacts.mkdir(parents=True)

def docker(*args, check=True):
    result = subprocess.run(["docker", *args], capture_output=True, text=True, encoding="utf-8", errors="replace")
    if check and result.returncode:
        raise RuntimeError("Docker " + args[0] + " failed: " + result.stderr[-1800:])
    return result.stdout.strip()

def put(name, content, target):
    path = artifacts / name
    path.write_bytes(content.encode("utf-8"))
    docker("cp", str(path), runner + ":" + target)
    docker("exec", runner, "chmod", "600", target)

def compose(*args, check=True):
    return docker("exec", runner, "docker", "compose", "--project-name", project,
                  "--project-directory", base, "-f", base + compose_file, *args, check=check)

def request(path, body=None):
    args = ["exec", runner, "curl", "--silent", "--show-error", "--unix-socket", base + socket_path,
            "-H", "Authorization: Bearer " + token, "-H", "Content-Type: application/json"]
    if body is not None:
        args += ["-X", "POST", "--data-binary", json.dumps(body)]
    return json.loads(docker(*args, "http://deployd" + path))

def task(protocol, enabled, port):
    generation = request("/v1/status")["generation"]
    body = {"protocol": protocol, "enabled": enabled, "bind": "" if protocol in ("graphics","rdp-drive","mp4") else "0.0.0.0" if protocol == "management" else "127.0.0.1",
            "port": port, "generation": generation}
    preflight = request("/v1/preflight", body)
    if not preflight.get("passed"):
        raise RuntimeError("Preflight " + protocol + ": " + str(preflight.get("error")))
    accepted = request("/v1/tasks", body)
    if "id" not in accepted:
        raise RuntimeError("Task submission failed: " + str(accepted.get("error")))
    deadline = time.monotonic() + 360
    while time.monotonic() < deadline:
        state = request("/v1/status")["task"]
        if state["id"] != accepted["id"]:
            raise RuntimeError("Unexpected concurrent task")
        if state["state"] != "running":
            if state["state"] != "succeeded":
                raise RuntimeError(protocol + " failed at " + state["stage"] + ": " + state.get("error", ""))
            print(protocol + (" enabled" if enabled else " disabled") + ": verified", flush=True)
            return accepted
        time.sleep(1)
    raise RuntimeError("Task timeout")

def start_executor():
    docker("exec", "-d", runner, "sh", "-c", 'echo $$ > "$1/daemon.pid"; exec "$1/puppeteer-deployd" --config "$1/deployd.json"', "sh", base)


def recover_crash(enabled, protocol="mysql"):
    generation = request("/v1/status")["generation"]
    body = {"protocol":protocol, "enabled":enabled, "bind":"" if protocol in ("graphics","rdp-drive","mp4") else "127.0.0.1", "port":0 if protocol in ("graphics","rdp-drive","mp4") else 25306, "generation":generation}
    put("inject", "once", base + "/inject-crash")
    accepted = request("/v1/tasks", body)
    if "id" not in accepted: raise RuntimeError("Crash test task rejected")
    for attempt in range(90):
        marker = docker("exec", runner, "sh", "-c", 'if [ -e "$1/inject-crash" ]; then echo waiting; else echo injected; fi', "sh", base)
        if marker == "injected": break
        time.sleep(1)
    else: raise RuntimeError("Crash injection did not reach apply")
    # Only the daemon recorded in this disposable runner is killed.
    pid = docker("exec", runner, "cat", base + "/daemon.pid")
    if not pid.isdigit() or int(pid) <= 1: raise RuntimeError("Invalid test daemon PID")
    docker("exec", runner, "kill", "-KILL", pid)
    start_executor()
    for attempt in range(180):
        try:
            current = request("/v1/status")["task"]
            if current["id"] == accepted["id"] and current["state"] != "running": break
        except (RuntimeError, json.JSONDecodeError): pass
        time.sleep(1)
    else: raise RuntimeError("Crash recovery timeout")
    if current["state"] != "failed" or current["stage"] != "interrupted" or "recovery failed" in current.get("error", ""):
        raise RuntimeError("Unexpected crash recovery outcome: " + current.get("error", ""))
    if protocol in ("graphics","rdp-drive","mp4"):
        result=json.loads(compose("exec","-T","server","/app/nt","deployment-verify",protocol,"false","","0"))
        if not result.get("passed"): raise RuntimeError("Graphics remained open after crash recovery")
        print("Interrupted " + protocol + " " + ("enable rolled back" if enabled else "disable fenced") + ": verified",flush=True)
        return
    inventory = compose("ps", "--format", "json", "server")
    server = json.loads(inventory)
    if isinstance(server,list): server=server[0]
    if any(p.get("TargetPort") == 13306 and p.get("PublishedPort") for p in server.get("Publishers",[]) or []):
        raise RuntimeError("MySQL remained published after recovery")
    result=json.loads(compose("exec","-T","server","/app/nt","deployment-verify","mysql","false","127.0.0.1","25306"))
    if not result.get("passed"): raise RuntimeError("MySQL listener remained open after recovery")
    print("Interrupted " + ("enable rolled back" if enabled else "disable fenced") + ": verified", flush=True)


def session_marker(name, suffix):
    for attempt in range(40):
        if docker("exec",runner,"sh","-c",'test -f "$1" && echo ready',"sh",base+"/"+name+"."+suffix,check=False)=="ready": return
        time.sleep(1)
    log=docker("exec",runner,"cat",base+"/"+name+".log",check=False)
    raise RuntimeError("VNC session "+suffix+" was not observed: "+log)

def start_graphics_session(name):
    docker("exec","-d",runner,"sh","-c",'python3 "$1/graphics_session_probe.py" "$1/$2" > "$1/$2.log" 2>&1',"sh",base,name)
    session_marker(name,"ready")

base = ""
compose_file = "/compose.json"
socket_path = "/runtime/deployd.sock"
started = False
enrolled_unit = ""
try:
    server_image = docker("image", "inspect", "--format", "{{.Id}}", arguments.server_image)
    docker("volume", "create", "--label", "puppeteer.p0-test=" + project, volume)
    base = json.loads(docker("volume", "inspect", volume))[0]["Mountpoint"]
    if not base.startswith("/var/lib/docker/volumes/") or not base.endswith("/_data"):
        raise RuntimeError("Unexpected disposable volume location")
    docker("run", "-d", "--name", runner, "--label", "puppeteer.p0-test=" + project,
           "--mount", "type=volume,source=" + volume + ",target=" + base,
           "--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock",
           *(["--device","/dev/fuse"] if arguments.drive and not arguments.systemd else []),
           *(["--privileged", "--cgroupns=private", "--tmpfs", "/run", "--tmpfs", "/run/lock", "--tmpfs", "/tmp", "puppeteer/deployd-systemd-test:local"] if arguments.systemd else
             ["--entrypoint", "sleep", "puppeteer/installer-test:local", "infinity"]))
    if arguments.systemd:
        for attempt in range(30):
            state = docker("exec", runner, "systemctl", "is-system-running", check=False)
            if state in ("running", "degraded"): break
            time.sleep(1)
        else: raise RuntimeError("Disposable systemd manager did not start")
    docker("exec", runner, "mkdir", "-p", base + "/state", base + "/runtime")
    docker("exec", runner, "chown", "0:10001", base + "/runtime")
    docker("exec", runner, "chmod", "750", base + "/runtime")
    if arguments.executor:
        binary = arguments.executor.resolve(strict=True)
    else:
        docker("create", "--name", extract, server_image)
        binary = artifacts / "puppeteer-deployd"
        docker("cp", extract + ":/app/puppeteer-deployd", str(binary))
        docker("rm", extract)
    docker("cp", str(binary), runner + ":" + base + "/puppeteer-deployd")
    docker("exec", runner, "chmod", "700", base + "/puppeteer-deployd")
    token = secrets.token_hex(32)
    put("token", token, base + "/token")
    put("state.json", json.dumps({"owned": {}}), base + "/state/deployment.json")
    nginx = "pid /tmp/nginx.pid;\nevents {}\nhttp { client_body_temp_path /tmp/client; proxy_temp_path /tmp/proxy; fastcgi_temp_path /tmp/fastcgi; uwsgi_temp_path /tmp/uwsgi; scgi_temp_path /tmp/scgi; server { listen 8443; include /etc/nginx/optional/*.conf; location = /healthz { return 200 'ok'; }\nlocation / {\nproxy_pass http://server:8089;\n} } }\n"
    put("nginx.conf", nginx, base + "/nginx.conf")
    docker("exec", runner, "chmod", "644", base + "/nginx.conf")
    spec = importlib.util.spec_from_file_location("enrollment", ROOT/"backend/deploy/enroll-deployd.py")
    enrollment = importlib.util.module_from_spec(spec); spec.loader.exec_module(enrollment)
    put("progress.conf", enrollment.PROGRESS_CONFIG, base + "/progress.conf")
    docker("exec", runner, "chmod", "644", base + "/progress.conf")
    wrapper = '#!/bin/sh\ncase "$*" in *" up -d "*" server") if [ -f "' + base + '/inject-crash" ]; then rm "' + base + '/inject-crash"; kill -STOP "$PPID"; exit 75; fi;; esac\nexec /usr/bin/docker "$@"\n'
    put("docker-executor", wrapper, base + "/docker-executor")
    docker("exec", runner, "chmod", "700", base + "/docker-executor")
    password = secrets.token_hex(16)
    model = {"name": project, "services": {
        "postgres": {"image": "postgres:18-alpine", "environment": {"POSTGRES_USER": "p0", "POSTGRES_DB": "p0", "POSTGRES_PASSWORD": password},
                     "healthcheck": {"test": ["CMD-SHELL", "pg_isready -U p0 -d p0"], "interval": "2s", "retries": 40}, "volumes": ["database:/var/lib/postgresql"]},
        "server": {"image": server_image, "environment": {
            "NT_ENV": "development", "NT_ADDR": ":8089", "NT_DATABASE_URL": "postgres://p0:" + password + "@postgres:5432/p0?sslmode=disable",
            "NT_MFA_KEY": secrets.token_hex(32), "NT_VAULT_KEY": secrets.token_hex(32), "NT_DATABASE_PROXY_NAME": "db.example.test",
            "NT_ALLOWED_ORIGINS": "http://backend.localhost:5174", "NT_MYSQL_SPOOL_DIR": "/tmp/mysql-spool"},
            "depends_on": {"postgres": {"condition": "service_healthy"}}, "volumes": ["appdata:/var/lib/puppeteer"],
            "healthcheck": {"test": ["CMD", "/app/nt", "health"], "interval": "2s", "retries": 50}},
        "web": {"image": arguments.web_image, "user":"101:101", "group_add":["10001"], "read_only":True, "tmpfs":["/tmp"], "entrypoint":["nginx","-g","daemon off;"], "volumes": [{"type": "bind", "source": base + "/nginx.conf", "target": "/etc/nginx/nginx.conf", "read_only": True}, {"type":"bind","source":base+"/runtime","target":"/run/puppeteer-deploy","read_only":True}, {"type":"bind","source":base+"/progress.conf","target":"/etc/nginx/optional/deployment-progress.conf","read_only":True}],
                "depends_on": {"server": {"condition": "service_healthy"}}, "healthcheck": {"test": ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1:8443/healthz"], "interval": "2s", "retries": 30}}},
        "volumes": {"database": {}, "appdata": {}}}
    if arguments.mp4:
        put("mp4_token",secrets.token_hex(32),base+"/mp4_token")
        docker("exec",runner,"chown","10001:10001",base+"/mp4_token")
        model['secrets']={'mp4_token':{'file':base+'/mp4_token'}}
        model['networks']={'default':{},'render':{'internal':True}}
        server=model['services']['server']
        server['networks']={'default':None,'render':None}
        server['environment'].update({'NT_MP4_URL':'http://mp4-renderer:8090','NT_MP4_TOKEN_FILE':'/run/secrets/mp4_token','NT_MP4_DIRECTORY':'/var/lib/puppeteer/mp4'})
        server['secrets']=[{'source':'mp4_token','target':'mp4_token'}]
        model['services']['mp4-renderer']={'image':docker('image','inspect','--format','{{.Id}}','cloners/puppeteer-renderer:r17-rc1'),
            'environment':{'NT_MP4_TOKEN_FILE':'/run/secrets/mp4_token'},'secrets':[{'source':'mp4_token','target':'mp4_token'}],
            'networks':{'render':None},'read_only':True,'cap_drop':['ALL'],'security_opt':['no-new-privileges:true'],
            'tmpfs':['/tmp:rw,size=768m,mode=1777'],'mem_limit':2147483648,'cpus':2,'pids_limit':160,
            'healthcheck':{'test':['CMD','node','-e',"fetch('http://127.0.0.1:8090/healthz').then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))"],'interval':'2s','retries':30},'restart':'unless-stopped'}
        if not arguments.systemd: enrollment.enroll_mp4(model)
    if arguments.graphics:
        model["services"]["server"]["environment"].update({"NT_GUACD_ADDR":"guacd:4822","NT_GUACD_RELAY_HOST":"server","NT_RDP_DRIVE_ENABLED":"false"})
        model["services"]["server"]["networks"]={"default":None,"graphics":None}
        model["services"]["guacd"]={"image":docker("image","inspect","--format","{{.Id}}","guacamole/guacd:1.6.0"),"entrypoint":["/opt/guacamole/sbin/guacd"],"command":["-f","-b","0.0.0.0"],"user":"1000:1000","networks":{"graphics":None},"healthcheck":{"test":["CMD-SHELL","nc -z 127.0.0.1 4822"],"interval":"2s","retries":30},"volumes":["graphicsdata:/preserved"],"restart":"unless-stopped"}
        model["volumes"]["graphicsdata"]={}
        model["networks"]={"default":{},"graphics":{"internal":True}}
        if arguments.drive:
            model['volumes']['storageconfig']={}
            model['networks']['database']={'internal':True}
            model['services']['postgres']['networks']={'default':None,'database':None}
            model['services']['server']['networks']['database']=None
            model['services']['server']['volumes'].append({'type':'volume','source':'storageconfig','target':'/var/lib/puppeteer/storage'})
            model['services']['server']['environment']['NT_RDP_DRIVE_ENABLED']='true'
            g=model['services']['guacd']
            g['image']=docker('image','inspect','--format','{{.Id}}','cloners/puppeteer-graphics:r17-rc1')
            g['environment']={'NT_DATABASE_URL':model['services']['server']['environment']['NT_DATABASE_URL'],'NT_STORAGE_PROFILE':'/var/lib/puppeteer/storage/active.json'}
            g['networks']={'database':None,'graphics':None}
            g['devices']=[{'source':'/dev/fuse','target':'/dev/fuse','permissions':'rwm'}]
            g['cap_drop']=['ALL'];g['cap_add']=['SYS_ADMIN','SETUID','SETGID','DAC_OVERRIDE']
            g['volumes']=[{'type':'volume','source':'storageconfig','target':'/var/lib/puppeteer/storage','read_only':True}]
            g['tmpfs']=['/tmp:rw,size=128m,mode=1777'];g['read_only']=True
        if arguments.vnc_fixture:
            docker("cp",str(arguments.vnc_fixture.resolve(strict=True)),runner+":"+base+"/vnc-fixture")
            docker("exec",runner,"chmod","755",base+"/vnc-fixture")
            docker("cp",str(ROOT/"tests/graphics_session_probe.py"),runner+":"+base+"/graphics_session_probe.py")
            model["services"]["vnc-fixture"]={"image":server_image,"entrypoint":["/fixture"],"networks":{"graphics":None},"volumes":[{"type":"bind","source":base+"/vnc-fixture","target":"/fixture","read_only":True}]}
        if not arguments.systemd: enrollment.enroll_graphics(model)
    if arguments.systemd:
        model["services"]["web"]["volumes"] = model["services"]["web"]["volumes"][:1]
    put("compose.json", json.dumps(model), base + "/compose.json")
    cfg = {"executor": {"Docker": base + "/docker-executor", "Directory": base, "Project": project, "Files": [base + "/compose.json"]},
           "stateDirectory": base + "/state", "socket": base + socket_path, "tokenFile": base + "/token", "socketGroup": 10001}
    put("deployd.json", json.dumps(cfg), base + "/deployd.json")
    started = True
    compose("up", "-d", "--no-build", "--pull", "never", "--wait", "--wait-timeout", "150")
    if arguments.systemd:
        # This manager owns the test executor; Docker itself runs on the test host.
        put("docker.service", "[Unit]\nDescription=External test Docker daemon\n[Service]\nType=oneshot\nExecStart=/usr/bin/docker version --format {{.Server.Version}}\nRemainAfterExit=yes\n", "/etc/systemd/system/docker.service")
        docker("exec",runner,"systemctl","daemon-reload")
        docker("exec", runner, "mkdir", "-p", base+"/config", base+"/helpers")
        wrapper='#!/bin/bash\nset -euo pipefail\nexec docker compose --project-directory "'+base+'" --project-name "'+project+'" -f "'+base+'/compose.json" "$@"\n'
        put("puppeteerctl",wrapper,base+"/puppeteerctl")
        docker("exec",runner,"chmod","700",base+"/puppeteerctl")
        server=compose("ps","-q","server")
        docker("cp",server+":/app/deployment/.",str(artifacts/"helpers"))
        docker("cp",str(artifacts/"helpers")+"/.",runner+":"+base+"/helpers")
        docker("exec",runner,"bash",base+"/helpers/enroll-deployd.sh",base)
        enrolled_unit="puppeteer-deployd-"+project+".service"
        compose_file="/compose-managed.json"
        socket_path="/config/deployd-run/deployd.sock"
        token=docker("exec",runner,"cat",base+"/config/deployd-token")
        if docker("exec",runner,"systemctl","is-active",enrolled_unit)!="active": raise RuntimeError("Enrolled systemd service inactive")
        print("Packaged enrollment under actual systemd: verified",flush=True)
    else:
        start_executor()
    for attempt in range(30):
        try:
            request("/v1/status")
            break
        except (RuntimeError, json.JSONDecodeError):
            time.sleep(1)
    else:
        raise RuntimeError("Executor socket did not become ready")
    if arguments.mp4:
        component=request('/v1/status')['components']['mp4']
        if not component['supported'] or component['state']!='running': raise RuntimeError('Renderer was not enrolled: '+str(component))
        compose('exec','-T','server','sh','-c','echo keep > /var/lib/puppeteer/mp4/deployment-keep')
        task('mp4',False,0)
        # Pending conversions remain durable while the renderer and admission are off.
        queued=compose('exec','-T','postgres','psql','-U','p0','-d','p0','-Atc',"INSERT INTO jobs(kind,state,reason) VALUES('recording.mp4','queued','component_paused') RETURNING id").splitlines()[0]
        compose('up','-d','--no-build','--pull','never','--wait','--wait-timeout','120')
        verify=json.loads(compose('exec','-T','server','/app/nt','deployment-verify','mp4','false','','0'))
        if not verify.get('passed'): raise RuntimeError('Ordinary compose startup reopened MP4')
        if arguments.recovery: recover_crash(True,'mp4')
        state=compose('exec','-T','postgres','psql','-U','p0','-d','p0','-Atc',"SELECT state FROM jobs WHERE id='"+queued+"'")
        if state!='queued': raise RuntimeError('Paused queue was lost')
        compose('exec','-T','postgres','psql','-U','p0','-d','p0','-c',"DELETE FROM jobs WHERE id='"+queued+"'")
        task('mp4',True,0)
        if request('/v1/status')['components']['mp4']['state']!='running': raise RuntimeError('Renderer running evidence missing')
        if arguments.recovery: recover_crash(False,'mp4')
        else: task('mp4',False,0)
        compose('exec','-T','server','test','-f','/var/lib/puppeteer/mp4/deployment-keep')
        if request('/v1/status')['components']['mp4']['state']!='stopped': raise RuntimeError('Renderer remains running')
        print('MP4 real sample conversion, disabled startup, durable queue and cache preservation: verified',flush=True)
    if arguments.graphics and not arguments.drive:
        components=request("/v1/status").get("components",{})
        if not components.get("graphics",{}).get("supported"): raise RuntimeError("Graphics was not automatically enrolled: "+str(components))
        if components["graphics"].get("state") != "running": raise RuntimeError("Running graphics observation missing")
        if arguments.vnc_fixture:
            docker("network","connect",project+"_graphics",runner)
            start_graphics_session("initial-session")
        compose("exec","-T","--user","0","guacd","touch","/preserved/keep")
        task("graphics",False,0)
        if request("/v1/status")["components"]["graphics"].get("state") != "stopped": raise RuntimeError("Stopped graphics observation missing")
        if arguments.vnc_fixture: session_marker("initial-session","closed")
        compose("up","-d","--no-build","--pull","never","--wait","--wait-timeout","120")
        verify=json.loads(compose("exec","-T","server","/app/nt","deployment-verify","graphics","false","","0"))
        if not verify.get("passed"): raise RuntimeError("Ordinary compose up reopened disabled graphics")
        print("Disabled graphics survives ordinary compose up: verified",flush=True)
        if arguments.recovery: recover_crash(True,"graphics")
        task("graphics",True,0)
        compose("exec","-T","guacd","test","-f","/preserved/keep")
        if arguments.vnc_fixture: start_graphics_session("reenabled-session")
        if arguments.recovery: recover_crash(False,"graphics")
        else: task("graphics",False,0)
        if arguments.vnc_fixture:
            session_marker("reenabled-session","closed")
            print("Representative VNC frame and existing session termination: verified",flush=True)
        print("Graphics RDP/VNC plugin handshake and persistent volume: verified",flush=True)
    if arguments.drive:
        if not request('/v1/status')['components']['rdp-drive']['supported']: raise RuntimeError('Drive was not enrolled')
        compose('exec','-T','server','sh','-c','echo preserved > /var/lib/puppeteer/storage/drive-keep')
        task('rdp-drive',False,0)
        compose('up','-d','--no-build','--pull','never','--wait','--wait-timeout','120')
        verify=json.loads(compose('exec','-T','server','/app/nt','deployment-verify','rdp-drive','false','','0'))
        if not verify.get('passed'): raise RuntimeError('Ordinary startup reopened drive')
        if arguments.recovery: recover_crash(True,'rdp-drive')
        task('rdp-drive',True,0)
        compose('exec','-T','guacd','test','-f','/var/lib/puppeteer/storage/drive-keep')
        compose('exec','-T','guacd','/usr/local/bin/puppeteer-drive','rdp-storage','verify')
        denied=compose('exec','-T','--user','1000:1000','guacd','sh','-c','if cat /mnt/nt-rdp/.deployment-probe; then exit 42; else echo denied; fi')
        if denied!='denied': raise RuntimeError('Diagnostic inode exposed to guacd')
        if request('/v1/status')['components']['rdp-drive']['state']!='running': raise RuntimeError('Mounted observation missing')
        if arguments.vnc_fixture:
            docker('network','connect',project+'_graphics',runner)
            start_graphics_session('drive-session')
        if arguments.recovery: recover_crash(False,'rdp-drive')
        else: task('rdp-drive',False,0)
        if arguments.vnc_fixture: session_marker('drive-session','closed')
        compose('exec','-T','server','test','-f','/var/lib/puppeteer/storage/drive-keep')
        if request('/v1/status')['components']['rdp-drive']['state']!='stopped': raise RuntimeError('Unmounted observation missing')
        print('Drive FUSE read/write, UID isolation, mount removal, session termination and data preservation: verified',flush=True)
    if not arguments.recovery_only and not arguments.graphics and not arguments.mp4:
        protocols = [] if arguments.systemd_smoke else [("mysql", 25306), ("postgres", 25432), ("ssh", 25022), ("website", 25443)]
        for protocol, port in protocols:
            task(protocol, True, port)
            task(protocol, False, port)
        if not arguments.systemd_smoke:
            task("management", True, 9089)
        latest = task("management", True, 8089)
        compose("stop", "server")
        progress_url = "http://127.0.0.1:8443/deployment-progress/" + latest["id"] + "?ticket=" + latest["progressToken"]
        progress = json.loads(compose("exec","-T","web","wget","-q","-O","-",progress_url))
        if progress.get("state") != "succeeded" or "progressToken" in progress:
            raise RuntimeError("Independent progress verification failed")
        compose("up","-d","--no-deps","--no-recreate","--wait","--wait-timeout","120","server")
        print("Non-root Nginx progress while API stopped: verified",flush=True)
    if (arguments.recovery or arguments.recovery_only) and not arguments.graphics and not arguments.mp4:
        recover_crash(True)
        task("mysql",True,25306)
        recover_crash(False)
    print("Real Docker deployment smoke test passed: " + project, flush=True)
except Exception:
    if started:
        for service in ("server","web"):
            (artifacts/(service+"-failure.log")).write_text(compose("logs","--no-color","--tail","80",service,check=False),encoding="utf-8")
        (artifacts/"progress-permissions.log").write_text(compose("exec","-T","web","sh","-c","id; ls -ldn /run/puppeteer-deploy; ls -ln /run/puppeteer-deploy",check=False),encoding="utf-8")
    raise
finally:
    if enrolled_unit:
        docker("exec",runner,"systemctl","stop",enrolled_unit,check=False)
    if started:
        compose("--profile", "*", "down", "--volumes", "--remove-orphans", check=False)
    for name in (extract, runner):
        docker("rm", "-f", name, check=False)
    if base:
        metadata = json.loads(docker("volume", "inspect", volume))[0]
        if metadata.get("Labels", {}).get("puppeteer.p0-test") == project:
            docker("volume", "rm", volume, check=False)
