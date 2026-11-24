#!/bin/sh
# Run from the new, separately extracted package; preserve the existing deployment.
set -eu
umask 077
package_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "$#" -ne 1 ]; then echo "用法: sh upgrade.sh /原部署目录" >&2; exit 1; fi
target=$(CDPATH= cd -- "$1" && pwd)
if [ "$package_dir" = "$target" ]; then echo '请将新版解压到独立目录，再指定原部署目录；不能先覆盖旧文件。' >&2; exit 1; fi
if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ]; then echo '仅支持 Linux amd64/x86_64。' >&2; exit 1; fi
for file in compose.yaml certs/server.crt certs/server.key certs/ca.crt; do
  test -s "$target/$file" || { echo "原部署缺少 $file" >&2; exit 1; }
done
cd "$package_dir"
sha256sum -c SHA256SUMS
docker compose version >/dev/null
docker info >/dev/null
docker load -i lan-chat-image.tar
architecture=$(docker image inspect lan-chat:2026.11.13-video-photo-amd64 --format '{{.Os}}/{{.Architecture}}')
[ "$architecture" = linux/amd64 ] || { echo "镜像架构错误: $architecture" >&2; exit 1; }
cd "$target"
container_id=$(docker compose ps -aq chat)
[ -n "$container_id" ] || { echo '未找到原 chat 容器，请先检查原部署目录。' >&2; exit 1; }
project=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}' "$container_id")
[ "$project" = lan-chat ] || { echo "只支持 lan-chat 项目；当前项目为 ${project}，未停服。" >&2; exit 1; }
data_volume=$(docker inspect --format '{{range .Mounts}}{{if and (eq .Destination "/data") (eq .Type "volume")}}{{.Name}}{{end}}{{end}}' "$container_id")
case "$data_volume" in ''|*[!a-zA-Z0-9_.-]*) echo '无法确认 /data 命名卷；绑定目录等自定义部署需手动迁移，未停服。' >&2; exit 1;; esac
backup="$target/backups/$(date +%Y%m%d-%H%M%S)-$$"
mkdir -p "$backup"
cp -a compose.yaml "$backup/compose.yaml"
cp -a certs "$backup/certs"
if [ -f .env ]; then cp -a .env "$backup/.env"; fi
docker inspect --format '{{.Image}}' "$container_id" > "$backup/image-id.txt"
# Keep a tag for the previous image, so ordinary image pruning cannot discard it.
rollback_tag="lan-chat:rollback-$(basename "$backup")"
docker tag "$(cat "$backup/image-id.txt")" "$rollback_tag"
printf '%s\n' "$rollback_tag" > "$backup/image-tag.txt"
phase=backup
cleanup() {
  status=$?
  trap - 0
  if [ "$status" -ne 0 ]; then
    if [ "$phase" = backup ]; then
      docker compose start chat || true
      echo '备份未完成，已尝试恢复原服务；原配置未被覆盖。' >&2
    else
      docker compose stop chat || true
      echo "升级未完成，服务已停止以保留数据。备份目录：${backup}；按升级说明回滚。" >&2
    fi
  fi
  exit "$status"
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' TERM
docker compose stop chat
docker cp "$container_id:/data" "$backup/data"
phase=upgrade
for file in compose.yaml install.sh upgrade.sh Dockerfile lanchat lan-chat-image.tar SHA256SUMS 部署说明.md 验证记录.md 升级说明.md; do
  cp "$package_dir/$file" "$target/$file"
done
cp -a "$package_dir/scripts" "$target/"
# Pin the actual mounted volume, including a volume restored during a rollback.
# This runtime configuration is intentionally different from the package template.
awk -v volume="$data_volume" '{print; if ($0 == "  chat-data:") {print "    external: true"; print "    name: " volume}}' compose.yaml > compose.yaml.new
mv compose.yaml.new compose.yaml
printf '%s\n' "$data_volume" > "$backup/data-volume.txt"
# Certificates and their ACLs are deliberately reused; this is not a re-install.
SERVER_IP= docker compose up -d --no-build --pull never --force-recreate
attempt=0
until docker compose exec -T chat /lanchat healthcheck >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then docker compose logs --tail=50 chat; exit 1; fi
  sleep 2
done
trap - 0 INT TERM
docker compose ps
printf '升级完成。旧记录已归入公共频道，证书与数据卷已保留。\n备份：%s\n' "$backup"
