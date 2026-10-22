#!/usr/bin/env python3
"""Hermetic regression tests for deploy.sh worker rollout and rollback."""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


DEPLOY_SCRIPT = Path(__file__).with_name("deploy.sh")
NEW_SHA = "a" * 40
OLD_SHA = "b" * 40
class DeployRolloutTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix="worknet-deploy-test-")
        self.root = Path(self.temp.name)
        self.app = self.root / "app"
        self.app.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.auth = self.root / "auth"
        self.auth.mkdir()
        (self.auth / "worknet-analytics.htpasswd").write_text("fixture\n")
        (self.auth / "worknet-admin-proxy.conf").write_text(
            "proxy_set_header X-Internal-Admin-Secret fixture;\n"
            "proxy_set_header X-Internal-Admin-User $remote_user;\n"
        )
        (self.app / "server.conf").write_text("INTERNAL_ADMIN_PROXY_SECRET=fixture\n")
        (self.app / "compose.production.yaml").write_text("services: {}\n")
        self.log = self.root / "docker.jsonl"

        # Redirect only the fixed auth precheck paths in an isolated copy.
        source = DEPLOY_SCRIPT.read_text().replace("/etc/nginx/auth", str(self.auth))
        self.script = self.root / "deploy.sh"
        self.script.write_text(source)
        self.script.chmod(0o755)

        self._write_executable(
            "docker",
            """#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
with open(os.environ['DOCKER_LOG'], 'a') as f:
    f.write(json.dumps({'args': args, 'backend': os.environ.get('BACKEND_IMAGE'), 'frontend': os.environ.get('FRONTEND_IMAGE')}) + '\\n')
if 'ps' in args:
    known = {'postgres', 'daily-notifications', 'event-sync-timepad-spb', 'event-sync'}
    requested = [arg for arg in args if arg in known]
    if requested and requested != ['postgres']:
        for service in requested:
            print(service if '--format' in args and '{{.Service}}' in args else service + '-container')
""",
        )
        self._write_executable(
            "curl",
            """#!/usr/bin/env python3
import os, sys
args = sys.argv[1:]
url = args[-1] if args else ''
if '--write-out' in args:
    print('401')
    raise SystemExit(0)
if os.environ.get('MOCK_HEALTH_FAIL') == '1' and url.endswith('/api/v1/health/ready'):
    raise SystemExit(22)
raise SystemExit(0)
""",
        )
        self._write_executable(
            "flock",
            """#!/usr/bin/env python3
import os, sys
raise SystemExit(1 if os.environ.get('MOCK_LOCK_FAIL') == '1' else 0)
""",
        )
        self._write_executable("sleep", "#!/bin/sh\nexit 0\n")

    def tearDown(self) -> None:
        self.temp.cleanup()

    def _write_executable(self, name: str, content: str) -> None:
        path = self.bin / name
        path.write_text(content)
        path.chmod(0o755)

    def run_deploy(self, *, previous: bool = False, health_fail: bool = False, lock_fail: bool = False):
        release_file = self.app / "release.state"
        if previous:
            release_file.write_text(f"RELEASE_SHA={OLD_SHA}\n")
        env = os.environ.copy()
        env.update(
            {
                "PATH": f"{self.bin}{os.pathsep}{env.get('PATH', '')}",
                "APP_DIR": str(self.app),
                "ENV_FILE": "server.conf",
                "COMPOSE_FILE": "compose.production.yaml",
                "RELEASE_FILE": "release.state",
                "BACKUP_DIR": str(self.root / "backups"),
                "PUBLIC_URL": "https://deploy.test",
                "IMAGE_PREFIX": "images.test/worknet",
                "DOCKER_LOG": str(self.log),
                "MOCK_HEALTH_FAIL": "1" if health_fail else "0",
                "MOCK_LOCK_FAIL": "1" if lock_fail else "0",
            }
        )
        result = subprocess.run(
            ["bash", str(self.script), NEW_SHA],
            cwd=self.root,
            env=env,
            text=True,
            capture_output=True,
            timeout=15,
        )
        records = [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []
        return result, records, release_file

    @staticmethod
    def worker_recreations(records):
        result = []
        for record in records:
            args = record["args"]
            if "up" in args and "--force-recreate" in args:
                result.append(next((service for service in ("event-sync-timepad-spb", "event-sync") if service in args), None))
        return result

    def test_deploy_recreates_spb_before_moscow_with_new_release(self):
        result, records, release_file = self.run_deploy()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.worker_recreations(records), ["event-sync-timepad-spb", "event-sync"])
        self.assertTrue(any("stop" in r["args"] and "event-sync-timepad-spb" in r["args"] and "event-sync" in r["args"] for r in records))
        image_records = [r for r in records if "pull" in r["args"] or "run" in r["args"] or "--force-recreate" in r["args"]]
        self.assertTrue(image_records)
        self.assertTrue(all(r["backend"] == f"images.test/worknet/backend:{NEW_SHA}" for r in image_records))
        self.assertEqual(release_file.read_text(), f"RELEASE_SHA={NEW_SHA}\n")

    def test_failed_smoke_check_rolls_back_both_workers_spb_first(self):
        result, records, release_file = self.run_deploy(previous=True, health_fail=True)
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr.count("smoke checks failed"), 1)
        self.assertEqual(
            self.worker_recreations(records),
            ["event-sync-timepad-spb", "event-sync", "event-sync-timepad-spb", "event-sync"],
        )
        pulls = [r for r in records if "pull" in r["args"]]
        self.assertEqual([r["backend"] for r in pulls], [f"images.test/worknet/backend:{NEW_SHA}", f"images.test/worknet/backend:{OLD_SHA}"])
        self.assertEqual(release_file.read_text(), f"RELEASE_SHA={OLD_SHA}\n")

    def test_deploy_lock_failure_makes_no_compose_changes(self):
        result, records, release_file = self.run_deploy(lock_fail=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("another deployment is running", result.stderr)
        self.assertEqual(records, [])
        self.assertFalse(release_file.exists())


if __name__ == "__main__":
    unittest.main()
