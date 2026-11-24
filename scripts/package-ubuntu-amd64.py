"""Build an offline amd64 release; include only explicitly selected source files."""
from pathlib import Path
import hashlib
import os
import shutil
import subprocess
import tarfile

ROOT=Path(__file__).resolve().parent.parent
VERSION='2026.11.13-video-photo-amd64'
IMAGE='lan-chat:'+VERSION
DIST=ROOT/'dist'
STAGE=DIST/'lan-chat-ubuntu-amd64'
DIST.mkdir(exist_ok=True)
STAGE.mkdir(exist_ok=True)
# Remove only this reproducible staging directory's old release files.
for path in STAGE.iterdir():
 if path.is_dir():shutil.rmtree(path)
 else:path.unlink()
for path in (ROOT/'deploy/ubuntu-amd64').iterdir():
 if path.is_file():shutil.copy2(path,STAGE/path.name)
(STAGE/'scripts').mkdir()
for name in ('host-ips.sh','generate-certs.sh'):shutil.copy2(ROOT/'scripts'/name,STAGE/'scripts'/name)
subprocess.run(['go','build','-trimpath','-ldflags=-s -w','-o',str(STAGE/'lanchat'),'./cmd/lanchat'],cwd=ROOT,env=dict(os.environ,CGO_ENABLED='0',GOOS='linux',GOARCH='amd64'),check=True)
subprocess.run(['docker','build','--platform','linux/amd64','-t',IMAGE,str(STAGE)],check=True)
architecture=subprocess.check_output(['docker','image','inspect',IMAGE,'--format','{{.Os}}/{{.Architecture}}'],text=True).strip()
if architecture!='linux/amd64':raise RuntimeError('Wrong image architecture: '+architecture)
subprocess.run(['docker','save','-o',str(STAGE/'lan-chat-image.tar'),IMAGE],check=True)
manifest=''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.relative_to(STAGE)}\n' for p in sorted(STAGE.rglob('*')) if p.is_file())
(STAGE/'SHA256SUMS').write_text(manifest)
def clean_metadata(info):
 info.uid=info.gid=0;info.uname=info.gname='root';info.pax_headers={};return info
archive=DIST/'lan-chat-ubuntu-amd64-20261113-video-photo.tar.gz'
with tarfile.open(archive,'w:gz') as tar:tar.add(STAGE,arcname='lan-chat-ubuntu-amd64',filter=clean_metadata)
checksum=hashlib.sha256(archive.read_bytes()).hexdigest()
archive.with_suffix(archive.suffix+'.sha256').write_text(f'{checksum}  {archive.name}\n')
# Explicit source allowlist excludes local runtime data and all credentials.
source_paths=['cmd','internal','web','scripts','deploy','docs','go.mod','go.sum','README.md','Dockerfile','compose.yaml','.dockerignore','.env.example','package.json','package-lock.json','playwright.config.js','tests']
with tarfile.open(DIST/'lan-chat-source.tar.gz','w:gz') as tar:
 for name in source_paths:
  path=ROOT/name
  if path.exists():tar.add(path,arcname='lan-chat/'+name,filter=lambda info: None if '__pycache__' in info.name or info.name.endswith('.pyc') else clean_metadata(info))
with tarfile.open(archive) as tar:
 for line in manifest.splitlines():
  expected,rel=line.split('  ',1)
  actual=hashlib.sha256(tar.extractfile('lan-chat-ubuntu-amd64/'+rel).read()).hexdigest()
  if actual!=expected:raise RuntimeError('Archive checksum mismatch: '+rel)
 for name in tar.getnames():
  if name.endswith(('.key','.db')) or '/certs/' in name or '/data/' in name:raise RuntimeError('Unexpected runtime file: '+name)
print(f'Release verified: {archive.name}, {archive.stat().st_size/1024/1024:.1f} MiB, SHA256 {checksum}')
