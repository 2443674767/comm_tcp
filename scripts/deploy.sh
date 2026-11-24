#!/bin/sh
# Run this on the Ubuntu server; no IP argument is needed by default.
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
server_ip=${1:-${SERVER_IP:-auto}}
docker compose version >/dev/null
sh scripts/generate-certs.sh "$server_ip"
if [ "$server_ip" = auto ]; then server_ip=; fi
# Override a previous deployment's .env IP; blank enables native ICE discovery.
SERVER_IP="$server_ip" docker compose up -d --build --force-recreate
docker compose ps
echo '请在客户端安装并信任 certs/ca.crt，然后使用上方当前主机的 HTTPS 地址访问。'
