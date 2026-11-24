"""Exercise host discovery and certificate generation without changing networking."""
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import shutil
import unittest

ROOT = Path(__file__).resolve().parent.parent


class DeploymentTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.work = Path(self.tmp.name)
        self.bin = self.work / "bin"
        self.bin.mkdir()
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"])
        self.env.pop("SERVER_IP", None)
        self.command("ip", '''case "$*" in
          "-4 route get 1.1.1.1") echo '1.1.1.1 via 10.20.30.1 dev eth0 src 10.20.30.40 uid 0';;
          "-o -4 addr show scope global") printf '%s\\n' '2: eth0 inet 10.20.30.40/24 scope global eth0' '3: eth1 inet 192.168.50.8/24 scope global eth1';;
          *) exit 1;; esac''')

    def command(self, name, body):
        path = self.bin / name
        path.write_text("#!/bin/sh\n" + body + "\n")
        path.chmod(0o755)

    def generate(self, ip="auto"):
        return subprocess.run(["sh", str(ROOT / "scripts/generate-certs.sh"), ip, str(self.work / "certs")],
                              env=self.env, capture_output=True, text=True)

    def verify_ip(self, ip):
        p = subprocess.run(["openssl", "verify", "-CAfile", str(self.work / "certs/ca.crt"),
                            "-verify_ip", ip, str(self.work / "certs/server.crt")], capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_auto_cert_covers_all_host_addresses_and_reuses_ca(self):
        p = self.generate()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("https://10.20.30.40:18086", p.stdout)
        for ip in ("10.20.30.40", "192.168.50.8", "127.0.0.1"):
            self.verify_ip(ip)
        before = hashlib.sha256((self.work / "certs/ca.crt").read_bytes()).hexdigest()
        self.command("ip", '''case "$*" in
          "-4 route get 1.1.1.1") echo '1.1.1.1 via 10.9.0.1 dev eth0 src 10.9.0.20';;
          "-o -4 addr show scope global") echo '2: eth0 inet 10.9.0.20/24 scope global eth0';;
          *) exit 1;; esac''')
        p = self.generate()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.verify_ip("10.9.0.20")
        self.assertEqual(before, hashlib.sha256((self.work / "certs/ca.crt").read_bytes()).hexdigest())

    def test_explicit_ip_still_supported(self):
        p = self.generate("172.20.10.30")
        self.assertEqual(p.returncode, 0, p.stderr)
        self.verify_ip("172.20.10.30")

    def test_no_argument_uses_current_host(self):
        p = subprocess.run(["sh", str(ROOT / "scripts/generate-certs.sh")],
                           cwd=self.work, env=self.env, capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.verify_ip("10.20.30.40")

    def test_deploy_prepares_certs_and_does_not_reuse_dotenv_ip(self):
        project = self.work / "project"
        project.mkdir()
        shutil.copytree(ROOT / "scripts", project / "scripts")
        (project / ".env").write_text("SERVER_IP=192.168.123.164\n")
        self.env["DEPLOY_LOG"] = str(self.work / "docker.log")
        self.command("docker", '''if [ "$2" = up ]; then
          test -s certs/server.crt && test -s certs/server.key || exit 2
          printf 'IP=<%s> %s\\n' "$SERVER_IP" "$*" >> "$DEPLOY_LOG"
        fi''')
        p = subprocess.run(["sh", str(project / "scripts/deploy.sh")],
                           env=self.env, capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("IP=<> compose up -d --build --force-recreate", (self.work / "docker.log").read_text())
        p = subprocess.run(["sh", str(project / "scripts/deploy.sh"), "10.8.0.9"],
                           env=self.env, capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn("IP=<10.8.0.9>", (self.work / "docker.log").read_text())

    def test_offline_host_needs_no_internet_route(self):
        self.command("ip", '''case "$*" in
          "-o -4 addr show scope global") echo '2: eth0 inet 192.168.88.20/24 scope global eth0';;
          *) exit 1;; esac''')
        p = self.generate()
        self.assertEqual(p.returncode, 0, p.stderr)
        self.verify_ip("192.168.88.20")

    def test_no_address_fails_without_generating_wrong_cert(self):
        self.command("ip", "exit 1")
        p = self.generate()
        self.assertNotEqual(p.returncode, 0)
        self.assertFalse((self.work / "certs/ca.key").exists())


if __name__ == "__main__":
    unittest.main()
