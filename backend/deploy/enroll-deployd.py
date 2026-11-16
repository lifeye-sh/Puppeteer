#!/usr/bin/env python3
"""Prepare private deployd enrollment artifacts from resolved Compose JSON.
No Docker commands or service changes are performed by this helper.
"""
import argparse
import json
import os
from pathlib import Path
import re
import secrets

PORTS = {"mysql": 13306, "postgres": 15432, "ssh": 2022, "website": 18443}

def enroll_upgrade(model):
    """Use the already enrolled, host-owned, read-only runtime directory."""
    server = model.get("services", {}).get("server", {})
    mounts = [v for v in server.get("volumes", []) if v.get("target") == "/run/puppeteer-deploy"]
    if len(mounts) != 1 or mounts[0].get("type") != "bind" or mounts[0].get("read_only") is not True:
        return False
    server.setdefault("environment", {})["NT_UPGRADE_HOLD"] = "/run/puppeteer-deploy/upgrade-hold"
    return True

def migrate_application_paths(model):
    """Remount existing named data volumes at the current product root.

    Discover the former root from the resolved runtime profile, never from a
    guessed installation name. Host bind sources, secrets and volume identities
    remain unchanged. Runtime/storage profiles contain credentials, not paths.
    """
    server = model.get("services", {}).get("server", {})
    profile = server.get("environment", {}).get("NT_RUNTIME_PROFILE", "")
    suffix = "/runtime/active.json"
    if not isinstance(profile, str) or not profile.endswith(suffix):
        return False
    old, new = profile[:-len(suffix)], "/var/lib/puppeteer"
    if old == new:
        return False
    if not re.fullmatch(r"/var/lib/[a-zA-Z0-9_-]+", old):
        raise ValueError("custom application root requires an explicit migration")
    if not any(v.get("type") == "volume" and v.get("target") == old
               and v.get("source") in ("appdata", "backupdata") for v in server.get("volumes", [])):
        raise ValueError("application root must use the existing named data volume")
    def path(value):
        return new + value[len(old):] if isinstance(value, str) and (value == old or value.startswith(old + "/")) else value
    def service(value):
        for key, item in value.get("environment", {}).items():
            value["environment"][key] = path(item)
        for volume in value.get("volumes", []):
            if isinstance(volume, dict):
                volume["target"] = path(volume.get("target"))
        if isinstance(value.get("tmpfs"), list):
            value["tmpfs"] = [path(v) for v in value["tmpfs"]]
    for item in model.get("services", {}).values():
        service(item)
    recipe = model.get("x-puppeteer-drive", {}).get("recipe")
    if isinstance(recipe, dict):
        service(recipe)
    return True

PROGRESS_CONFIG = """location ^~ /deployment-progress/ {
    limit_except GET { deny all; }
    access_log off;
    proxy_pass http://unix:/run/puppeteer-deploy/deployd.sock;
    proxy_set_header Authorization "";
    proxy_set_header Cookie "";
    proxy_buffering off;
    proxy_read_timeout 10s;
    add_header Cache-Control "no-store" always;
    add_header Referrer-Policy "no-referrer" always;
}
location ^~ /upgrade-progress/ {
    limit_except GET { deny all; }
    access_log off;
    error_log /dev/null;
    proxy_pass http://unix:/run/puppeteer-deploy/upgrade.sock;
    proxy_set_header Authorization "";
    proxy_set_header Cookie "";
    proxy_buffering off;
    proxy_read_timeout 10s;
    add_header Cache-Control "no-store" always;
    add_header Referrer-Policy "no-referrer" always;
}
"""

def enroll_graphics(model):
    """Adopt only a local, unprofiled, internal guacd recipe; preserve custom deployments."""
    enroll_operations(model)
    enroll_mp4(model)
    if "x-puppeteer-graphics" in model:
        return enroll_drive(model)  # Preserve stopped state; add compatible drive metadata.
    services = model.get("services", {})
    server, graphics = services.get("server", {}), services.get("guacd", {})
    env = server.get("environment", {})
    network = model.get("networks", {}).get("graphics", {})
    if (not graphics or env.get("NT_GUACD_ADDR") != "guacd:4822"
            or env.get("NT_GUACD_RELAY_HOST") != "server"
            or graphics.get("ports") or graphics.get("profiles") or graphics.get("network_mode")
            or not network.get("internal") or network.get("external")
            or "graphics" not in graphics.get("networks", {}) or "graphics" not in server.get("networks", {})
            or not graphics.get("healthcheck", {}).get("test")):
        return model
    if any("guacd" in service.get("depends_on", {}) for name, service in services.items() if name != "guacd"):
        return model
    drive = str(env.get("NT_RDP_DRIVE_ENABLED", "false")).lower()
    if drive not in ("true", "false"):
        return model
    labels = graphics.setdefault("labels", {})
    if "io.puppeteer.managed.graphics" in labels:
        return model
    labels["io.puppeteer.managed.graphics"] = "v1"
    model["x-puppeteer-graphics"] = {"version": 1, "driveEnabled": drive}
    return enroll_drive(model)


def enroll_operations(model):
    """Preserve existing operation intent; only enroll standard local PG18."""
    if "x-puppeteer-operations" in model:
        return model
    services = model.get("services", {})
    if not services.get("web") or not services.get("server"):
        return model
    pg = services.get("postgres", {})
    env = pg.get("environment", {})
    wal = (str(pg.get("image", "")).startswith("postgres:18-")
           and not pg.get("command") and not pg.get("network_mode") and not pg.get("ports")
           and env.get("POSTGRES_USER") == "puppeteer" and env.get("POSTGRES_DB") == "puppeteer"
           and any(v.get("type") == "volume" and v.get("target") == "/var/lib/postgresql" for v in pg.get("volumes", [])))
    model["x-puppeteer-operations"] = {"version": 1, "wal": bool(wal)}
    return model


def enroll_mp4(model):
    """Adopt the local renderer without changing credentials, data or stopped state."""
    if "x-puppeteer-mp4" in model:
        return model
    services = model.get("services", {})
    server, renderer = services.get("server", {}), services.get("mp4-renderer", {})
    env, renv = server.get("environment", {}), renderer.get("environment", {})
    network = model.get("networks", {}).get("render", {})
    if (not renderer or env.get("NT_MP4_URL") != "http://mp4-renderer:8090"
            or env.get("NT_MP4_TOKEN_FILE") != "/run/secrets/mp4_token"
            or renv.get("NT_MP4_TOKEN_FILE") != "/run/secrets/mp4_token"
            or env.get("NT_MP4_TOKEN") or renv.get("NT_MP4_TOKEN")
            or env.get("NT_MP4_DIRECTORY") != "/var/lib/puppeteer/mp4"
            or renderer.get("ports") or renderer.get("profiles") or renderer.get("network_mode")
            or renderer.get("privileged") or renderer.get("devices") or renderer.get("volumes")
            or renderer.get("command") or renderer.get("entrypoint")
            or not network.get("internal") or network.get("external")
            or "render" not in server.get("networks", {}) or "render" not in renderer.get("networks", {})
            or not renderer.get("healthcheck", {}).get("test")):
        return model
    for name, service in services.items():
        if name != "server" and "mp4-renderer" in service.get("depends_on", {}):
            return model
    if "io.puppeteer.managed.mp4" in renderer.get("labels", {}):
        return model
    renderer.setdefault("labels", {})["io.puppeteer.managed.mp4"] = "v1"
    model["x-puppeteer-mp4"] = {"version": 1}
    server.get("depends_on", {}).pop("mp4-renderer", None)
    return model


def enroll_drive(model):
    if "x-puppeteer-drive" in model or "x-puppeteer-graphics" not in model:
        return model
    service = model.get("services", {}).get("guacd", {})
    env = service.get("environment", {})
    devices = service.get("devices", [])
    volumes = service.get("volumes", [])
    if (service.get("privileged") or len(devices) != 1 or not isinstance(devices[0], dict) or devices[0].get("source") != "/dev/fuse"
            or len(volumes) != 1 or not isinstance(volumes[0], dict) or volumes[0].get("type") != "volume"
            or volumes[0].get("target") != "/var/lib/puppeteer/storage" or not volumes[0].get("read_only")
            or env.get("NT_STORAGE_PROFILE") != "/var/lib/puppeteer/storage/active.json"):
        return model
    keys = ("environment", "secrets", "networks", "volumes", "devices", "cap_add")
    model["x-puppeteer-drive"] = {"version": 1, "recipe": {key: service[key] for key in keys if key in service}}
    return model


def prepare(model, root, docker):
    root = Path(root)
    if not root.is_absolute() or not Path(docker).is_absolute():
        raise ValueError("absolute installation and Docker paths required")
    project = model.get("name", "")
    if not re.fullmatch(r"[a-z][a-z0-9-]{2,40}", project):
        raise ValueError("invalid resolved Compose project name")
    server = model.get("services", {}).get("server")
    if not server or "web" not in model["services"]:
        raise ValueError("server and web services required")
    if server.get("network_mode"):
        raise ValueError("network_mode enrollment is unsupported")
    owned = {}
    for protocol, target in PORTS.items():
        matches = [p for p in server.get("ports", []) if p.get("target") == target]
        if len(matches) > 1:
            raise ValueError("multiple protocol mappings require reconciliation")
        if matches:
            mapping = matches[0]
            if mapping.get("protocol", "tcp") != "tcp":
                raise ValueError("protocol mapping must use TCP")
            published = int(mapping["published"])
            if not 1 <= published <= 65535:
                raise ValueError("invalid published port")
            owned[protocol] = {"bind": mapping.get("host_ip") or "0.0.0.0",
                               "published": published, "target": target}
    runtime = root / "config" / "deployd-run"
    state = root / "config" / "deployd-state"
    token = root / "config" / "deployd-token"
    active = root / "compose-managed.json"
    volumes = server.setdefault("volumes", [])
    for target in ("/run/puppeteer-deploy", "/run/secrets/deployd_token"):
        if any(v.get("target") == target for v in volumes):
            raise ValueError("reserved executor mount already exists")
    volumes.extend([
        {"type": "bind", "source": str(runtime), "target": "/run/puppeteer-deploy", "read_only": True},
        {"type": "bind", "source": str(token), "target": "/run/secrets/deployd_token", "read_only": True},
    ])
    web = model["services"]["web"]
    web_volumes = web.setdefault("volumes", [])
    reserved = {"/run/puppeteer-deploy", "/etc/nginx/optional/deployment-progress.conf"}
    if any(v.get("target") in reserved for v in web_volumes):
        raise ValueError("reserved progress mount already exists")
    web_volumes.extend([
        {"type": "bind", "source": str(runtime), "target": "/run/puppeteer-deploy", "read_only": True},
        {"type": "bind", "source": str(root / "config/deployment-progress.conf"),
         "target": "/etc/nginx/optional/deployment-progress.conf", "read_only": True},
    ])
    groups = web.setdefault("group_add", [])
    if "10001" not in [str(group) for group in groups]:
        groups.append("10001")
    env = server.setdefault("environment", {})
    env["NT_DEPLOY_SOCKET"] = "/run/puppeteer-deploy/deployd.sock"
    env["NT_DEPLOY_TOKEN_FILE"] = "/run/secrets/deployd_token"
    enroll_upgrade(model)
    migrate_application_paths(model)
    enroll_graphics(model)
    # Existing mappings are enrolled, but listener policy is not changed until an operation.
    def escape(value):
        if isinstance(value, str):
            return value.replace("$", "$$")
        if isinstance(value, list):
            return [escape(v) for v in value]
        if isinstance(value, dict):
            return {k: escape(v) for k, v in value.items()}
        return value
    config = {"executor": {"Docker": docker, "Directory": str(root), "Project": project,
                            "Files": [str(active)]}, "stateDirectory": str(state),
              "socket": str(runtime / "deployd.sock"), "tokenFile": str(token), "socketGroup": 10001}
    return escape(model), config, {"owned": owned}

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", required=True)
    parser.add_argument("--directory", required=True)
    parser.add_argument("--docker", required=True)
    args = parser.parse_args()
    root = Path(args.directory)
    model, config, state = prepare(json.loads(Path(args.source).read_text()), root, args.docker)
    paths = [root / "compose-managed.json", root / "config/deployd.json", root / "config/deployd-token",
             root / "config/deployd-state", root / "config/deployd-run", root / "config/deployment-progress.conf"]
    if any(p.exists() or p.is_symlink() for p in paths):
        raise ValueError("enrollment already exists; refusing overwrite")
    (root / "config/deployd-state").mkdir(mode=0o700)
    (root / "config/deployd-run").mkdir(mode=0o750)
    os.chown(root / "config/deployd-run", 0, 10001)
    # Enrollment runs with umask 077; mkdir(mode=0750) alone becomes 0700.
    os.chmod(root / "config/deployd-run", 0o750)
    artifacts = {paths[0]: json.dumps(model), paths[1]: json.dumps(config),
                 paths[2]: secrets.token_hex(32), paths[3] / "deployment.json": json.dumps(state)}
    for path, content in artifacts.items():
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
    paths[5].write_text(PROGRESS_CONFIG)
    os.chmod(paths[5], 0o644)
    os.chown(paths[2], 0, 10001)
    os.chmod(paths[2], 0o640)

if __name__ == "__main__":
    main()
