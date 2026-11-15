"""Run P2 operations against a disposable Docker project, never a production stack.
Build backend/.local/p2-operations.test with CGO_ENABLED=0 go test -c first.
Requires the existing puppeteer/installer-test:local Docker CLI test runner.
"""
import json
import os
from pathlib import Path
import secrets
import subprocess

ROOT = Path(__file__).resolve().parents[1]
project = "puppeteer-p2-" + secrets.token_hex(5)
runner = project + "-runner"
volume = project + "-config"


def docker(*args, check=True):
    p = subprocess.run(["docker", *args], capture_output=True, text=True, encoding="utf-8", errors="replace")
    if check and p.returncode:
        raise RuntimeError(p.stdout[-3000:] + p.stderr[-3000:])
    return p.stdout.strip()


try:
    docker("volume", "create", "--label", "puppeteer.p2-test=" + project, volume)
    base = json.loads(docker("volume", "inspect", volume))[0]["Mountpoint"]
    if not base.startswith("/var/lib/docker/volumes/") or not base.endswith("/_data"):
        raise RuntimeError("unexpected isolated volume mountpoint")
    docker("run", "-d", "--name", runner, "--mount", "type=volume,source=" + volume + ",target=" + base,
           "--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock",
           "-e", "NT_OPERATIONS_DOCKER_ROOT=" + base, "-e", "NT_OPERATIONS_DOCKER_PROJECT=" + project,
           "-e", "NT_OPERATIONS_DOCKER_RUNNER=" + runner, "-e", "NT_OPERATIONS_ONLY_WAL=" + os.environ.get("NT_OPERATIONS_ONLY_WAL", ""), "puppeteer/installer-test:local")
    docker("cp", str(ROOT / "backend/.local/p2-operations.test"), runner + ":/tmp/operations.test")
    docker("exec", runner, "chmod", "700", "/tmp/operations.test")
    print("Disposable project:", project, flush=True)
    p = subprocess.run(["docker", "exec", runner, "/tmp/operations.test", "-test.run", "^TestOperationsRealDocker$", "-test.v", "-test.timeout", "42m"])
    if p.returncode:
        docker("exec", runner, "sh", "-c", 'for f in "$1"/state/*status.json; do test ! -f "$f" || cat "$f"; done', "sh", base, check=False)
        raise RuntimeError("P2 Docker acceptance failed")
finally:
    # Only this randomly named test project's containers/volumes are removed.
    for container in docker("ps", "-aq", "--filter", "label=com.docker.compose.project=" + project, check=False).splitlines():
        if container:
            docker("rm", "-f", container, check=False)
    for container in docker("ps", "-aq", "--filter", "label=io.puppeteer.wal-drill=" + project, check=False).splitlines():
        if container:
            docker("rm", "-f", container, check=False)
    docker("rm", "-f", runner, check=False)
    for name in docker("volume", "ls", "--format", "{{.Name}}", check=False).splitlines():
        if name.startswith(project + "_") or name.startswith(project + "-wal-drill-") or name == volume:
            docker("volume", "rm", name, check=False)
    docker("network", "rm", project + "_default", check=False)
