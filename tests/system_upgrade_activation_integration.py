"""Real isolated PostgreSQL/server upgrade-hold acceptance (no published ports).

Requires cached beta0.10 base, postgres:18-alpine, and Linux binaries built at
backend/.local/system-update/{server,application.test}. Does not run a complete
host upgrade or touch existing Compose projects.
"""
import json
import secrets
import subprocess
import time
from pathlib import Path


def main():
    root = Path(__file__).resolve().parents[1]
    work = root / "backend/.local/system-update"
    for binary in ("server", "application.test"):
        if not (work / binary).is_file():
            raise RuntimeError("Build the Linux " + binary + " fixture first")
    name = "puppeteer-upgrade-activation-" + secrets.token_hex(5)
    artifacts = work / name
    artifacts.mkdir()
    hold = artifacts / "upgrade-hold"
    hold.write_text("verification pending\n", encoding="utf-8")
    db, server = name + "-pg", name + "-server"

    def docker(*args, check=True):
        p = subprocess.run(["docker", *args], capture_output=True, text=True,
                           encoding="utf-8", errors="replace")
        if check and p.returncode:
            raise RuntimeError("Docker " + args[0] + " failed: " + p.stderr[-1600:])
        return p.stdout.strip()

    def sql(query):
        return docker("exec", db, "psql", "-U", "fixture", "-d", "fixture", "-Atc", query)

    def ready():
        raw = docker("exec", server, "wget", "-q", "-T", "2", "-O", "-",
                     "http://127.0.0.1:8089/readyz", check=False)
        try:
            return json.loads(raw)
        except ValueError:
            return {}

    try:
        docker("network", "create", "--internal", name)
        docker("run", "-d", "--pull", "never", "--name", db, "--network", name,
               "--network-alias", "postgres", "--memory", "384m", "--cpus", "1",
               "-e", "POSTGRES_USER=fixture", "-e", "POSTGRES_PASSWORD=isolated-test-only",
               "-e", "POSTGRES_DB=fixture", "postgres:18-alpine")
        for _ in range(40):
            p = subprocess.run(["docker", "exec", db, "pg_isready", "-U", "fixture"],
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if p.returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError("Isolated database did not become ready")

        dsn = "postgres://fixture:isolated-test-only@postgres:5432/fixture?sslmode=disable"
        with (artifacts / "application-tests.log").open("w", encoding="utf-8") as log:
            subprocess.run([
                "docker", "run", "--rm", "--pull", "never", "--network", name,
                "-e", "NT_TEST_DATABASE_URL=" + dsn,
                "-e", "NT_BACKUP_PG_TEST_DSN=" + dsn, "-e", "NT_R11_CLI_TEST_BINARY=/app/nt",
                "-v", str(work) + ":/tests:ro",
                "--entrypoint", "/tests/application.test", "cloners/puppeteer-server:beta0.10",
                "-test.run", "^(TestR11EncryptedDatabaseRoundTrip|TestSystemUpdatesAuthorizationAndCompatibility|TestSystemUpdateNotificationDedupAndRecipientAuthorization|TestUpgradeControlAuthenticationAndDrain|TestUpgradeHoldPreservesQueuedBackup|TestUpgradeFrozenStartupAndActivation|TestUpgradeActivationProfileAndHold|TestR15ToolApprovalRevokeRejectAndExpiry|TestR16MonitoringTransitionsAndDelivery|TestR16NotificationEntitlementNoDispatchAndInflight)$",
                "-test.v", "-test.timeout", "360s",
            ], stdout=log, stderr=subprocess.STDOUT, check=True)
        print("Application authorization and frozen activation integration passed.", flush=True)

        env = {
            "NT_ENV": "production", "NT_DATABASE_URL": dsn,
            "NT_DATABASE_NETWORK": "private-container", "NT_MFA_KEY": "2a" * 32,
            "NT_VAULT_KEY": "2b" * 32, "NT_ADDR": "0.0.0.0:8089",
            "NT_ALLOWED_ORIGINS": "https://localhost", "NT_BACKUP_DIRECTORY": "/var/lib/puppeteer/backups",
            "NT_RESTORE_DIRECTORY": "/var/lib/puppeteer/restore", "NT_MP4_DIRECTORY": "/var/lib/puppeteer/mp4",
            "NT_RUNTIME_PROFILE": "/var/lib/puppeteer/runtime/active.json",
            "NT_STORAGE_PROFILE": "/var/lib/puppeteer/storage/active.json",
            "NT_UPGRADE_HOLD": "/run/upgrade/upgrade-hold",
        }
        args = ["run", "-d", "--pull", "never", "--name", server, "--network", name,
                "--memory", "384m", "--cpus", "1", "-v", str(work / "server") + ":/app/server:ro",
                "-v", str(artifacts) + ":/run/upgrade:ro"]
        for key, value in env.items():
            args += ["-e", key + "=" + value]
        docker(*args, "cloners/puppeteer-server:beta0.10")
        for _ in range(60):
            status = ready()
            if status.get("status") == "ready":
                break
            time.sleep(1)
        else:
            raise RuntimeError("Frozen server did not become ready")
        assert status["frozen"] is True and status["upgradeHold"] is True
        assert sql("SELECT count(*) FROM backup_config WHERE recipient <> ''") == "0"
        # An entire real process restart still observes the host's durable hold.
        docker("restart", server)
        for _ in range(30):
            status = ready()
            if status.get("status") == "ready":
                break
            time.sleep(1)
        assert status.get("frozen") is True and status.get("upgradeHold") is True
        assert sql("SELECT count(*) FROM backup_config WHERE recipient <> ''") == "0"
        hold.unlink()
        for _ in range(30):
            status = ready()
            if status.get("frozen") is False:
                break
            time.sleep(1)
        assert status.get("frozen") is False and status.get("upgradeHold") is False
        assert sql("SELECT count(*) FROM backup_config WHERE recipient <> ''") == "1"
        assert sql("SELECT count(*) FROM configuration_secrets WHERE starts_with(key,'backup-recovery:')") == "1"
        result = {"isolated": True, "startupFrozen": True, "restartRetainsHold": True,
                  "commitActivates": True, "initializationDeferred": True, "fullHostUpgradeTested": False}
        (artifacts / "acceptance.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(result), flush=True)
    finally:
        (artifacts / "server.log").write_text(docker("logs", server, check=False), encoding="utf-8")
        # Docker sends application logs on stderr as well as stdout.
        with (artifacts / "server-combined.log").open("w", encoding="utf-8") as log:
            subprocess.run(["docker", "logs", server], stdout=log, stderr=subprocess.STDOUT)
        docker("rm", "-f", "-v", server, check=False)
        docker("rm", "-f", "-v", db, check=False)
        docker("network", "rm", name, check=False)


if __name__ == "__main__":
    main()
