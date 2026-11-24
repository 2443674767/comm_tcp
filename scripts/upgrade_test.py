"""Verify offline upgrade ordering and error recovery without touching Docker."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT=Path(__file__).resolve().parent.parent

class UpgradeTest(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup)
  self.work=Path(self.tmp.name);self.package=self.work/'new';self.target=self.work/'existing';self.bin=self.work/'bin'
  for path in (self.package,self.target,self.bin):path.mkdir()
  self.log=self.work/'actions'
  self.env=dict(os.environ,PATH=str(self.bin)+os.pathsep+os.environ['PATH'],UPGRADE_LOG=str(self.log))
  self.command('uname','case "$1" in -s) echo Linux;; -m) echo x86_64;; esac')
  self.command('sleep', ':')
  self.command('sha256sum','[ "${FAIL_CHECKSUM:-}" != yes ]')
  self.command('docker','''printf '%s\\n' "$*" >> "$UPGRADE_LOG"
case "$*" in
 "compose ps -aq chat") echo testcontainer;;
 "image inspect "*) echo linux/amd64;;
 "inspect --format {{.Image}} testcontainer") echo sha256:previous;;
 *com.docker.compose.project*) echo "${TEST_PROJECT:-lan-chat}";;
 *Mounts*) echo "${TEST_VOLUME:-lan-chat_chat-data}";;
 cp*) [ "${FAIL_BACKUP:-}" != yes ] || exit 7; mkdir -p "$3/audio"; echo legacy > "$3/chat.db";;
 "compose exec -T chat /lanchat healthcheck") [ "${FAIL_HEALTH:-}" != yes ];;
esac''')
  for name in ['compose.yaml','install.sh','Dockerfile','lanchat','lan-chat-image.tar','SHA256SUMS','部署说明.md','验证记录.md','升级说明.md']:(self.package/name).write_text('new '+name)
  (self.package/'compose.yaml').write_text('name: lan-chat\nvolumes:\n  chat-data:\n')
  (self.package/'scripts').mkdir();(self.package/'scripts/host-ips.sh').write_text('echo 10.0.0.20\n')
  (self.target/'compose.yaml').write_text('old compose');(self.target/'certs').mkdir()
  for name in ['ca.crt','ca.key','server.crt','server.key']:(self.target/'certs'/name).write_text('keep '+name)
  self.script=self.package/'upgrade.sh';shutil.copy(ROOT/'deploy/ubuntu-amd64/upgrade.sh',self.script)
 def command(self,name,body):
  p=self.bin/name;p.write_text('#!/bin/sh\n'+body+'\n');p.chmod(0o755)
 def run_upgrade(self):
  return subprocess.run(['sh',str(self.script),str(self.target)],env=self.env,capture_output=True,text=True)
 def test_backup_before_recreate_and_keep_certificates(self):
  result=self.run_upgrade();self.assertEqual(result.returncode,0,result.stderr)
  log=self.log.read_text();self.assertLess(log.index('compose stop chat'),log.index('cp testcontainer:/data'));self.assertLess(log.index('cp testcontainer:/data'),log.index('compose up'))
  self.assertIn('name: lan-chat', (self.target/'compose.yaml').read_text())
  self.assertEqual((self.target/'certs/server.key').read_text(),'keep server.key')
  backups=list((self.target/'backups').iterdir());self.assertEqual(len(backups),1)
  self.assertEqual((backups[0]/'data/chat.db').read_text(),'legacy\n');self.assertEqual((backups[0]/'compose.yaml').read_text(),'old compose')
 def test_keeps_active_restore_volume_on_next_upgrade(self):
  self.env['TEST_VOLUME']='lan-chat_restore_20261110'
  result=self.run_upgrade();self.assertEqual(result.returncode,0,result.stderr)
  self.assertIn('name: lan-chat_restore_20261110',(self.target/'compose.yaml').read_text())
  self.assertIn('external: true',(self.target/'compose.yaml').read_text())
 def test_rejects_other_project_before_stopping(self):
  self.env['TEST_PROJECT']='unrelated'
  result=self.run_upgrade();self.assertNotEqual(result.returncode,0)
  self.assertNotIn('compose stop',self.log.read_text())
 def test_health_failure_keeps_backup_and_stops_new_service(self):
  self.env['FAIL_HEALTH']='yes';result=self.run_upgrade();self.assertNotEqual(result.returncode,0)
  self.assertIn('升级未完成',result.stderr)
  self.assertEqual(self.log.read_text().count('compose stop chat'),2)
  self.assertTrue(list((self.target/'backups').glob('*/data/chat.db')))
 def test_rejects_unsupported_mount_before_stopping(self):
  self.env['TEST_VOLUME']='bind:/data';result=self.run_upgrade();self.assertNotEqual(result.returncode,0)
  self.assertNotIn('compose stop',self.log.read_text())
 def test_backup_failure_restarts_original_without_overwriting(self):
  self.env['FAIL_BACKUP']='yes';result=self.run_upgrade();self.assertNotEqual(result.returncode,0)
  self.assertEqual((self.target/'compose.yaml').read_text(),'old compose');self.assertIn('compose start chat',self.log.read_text());self.assertNotIn('compose up',self.log.read_text())
 def test_checksum_failure_never_stops_service(self):
  self.env['FAIL_CHECKSUM']='yes';result=self.run_upgrade();self.assertNotEqual(result.returncode,0)
  self.assertNotIn('compose stop',self.log.read_text() if self.log.exists() else '')
 def test_reject_same_directory_before_stopping(self):
  result=subprocess.run(['sh',str(self.script),str(self.package)],env=self.env,capture_output=True,text=True)
  self.assertNotEqual(result.returncode,0);self.assertNotIn('compose stop',self.log.read_text() if self.log.exists() else '')

if __name__=='__main__':unittest.main()
