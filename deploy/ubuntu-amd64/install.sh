#!/bin/sh
# Offline installation: run on the destination Ubuntu x86_64 host.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ]; then
  echo '此部署包仅用于 Linux amd64/x86_64（Ubuntu），不能在 macOS/ARM 上直接安装。' >&2
  exit 1
fi
for tool in docker openssl ip sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || { echo "缺少 $tool，请先安装所需依赖。" >&2; exit 1; }
done
docker compose version >/dev/null
docker info >/dev/null
sha256sum -c SHA256SUMS
docker load -i lan-chat-image.tar
architecture=$(docker image inspect lan-chat:2026.11.13-video-photo-amd64 --format '{{.Os}}/{{.Architecture}}')
if [ "$architecture" != linux/amd64 ]; then
  echo "镜像架构错误：$architecture" >&2; exit 1
fi
server_ip=${1:-${SERVER_IP:-auto}}
sh scripts/generate-certs.sh "$server_ip"
if [ "$server_ip" = auto ]; then server_ip=; fi
SERVER_IP="$server_ip" docker compose up -d --no-build --pull never --force-recreate
attempt=0
until docker compose exec -T chat /lanchat healthcheck >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    docker compose logs --tail=50 chat
    echo '服务未通过健康检查，请检查端口占用及以上日志。' >&2
    exit 1
  fi
  sleep 2
done
docker compose ps
echo '部署完成。请在使用设备上安装并信任 certs/ca.crt，然后打开上方 HTTPS 地址。'
