#!/bin/sh
# Generates a private LAN CA. Never installs it into any trust store.
set -eu
umask 077
server_ip=${1:-${SERVER_IP:-auto}}
cert_dir=${2:-./certs}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "$server_ip" = auto ]; then
  host_ips=$(sh "$script_dir/host-ips.sh")
  server_ip=$(printf '%s\n' "$host_ips" | head -n 1)
else
  host_ips=$server_ip
fi
if ! printf '%s\n' "$server_ip" | awk -F. 'NF!=4 {exit 1} {for(i=1;i<=4;i++) if($i!~/^[0-9]+$/ || $i>255) exit 1}'; then
  echo '服务器 IP 必须是有效 IPv4 地址' >&2; exit 1
fi
subject_alternatives=$(printf '%s\n127.0.0.1\n' "$host_ips" | awk '!seen[$0]++{printf "%sIP:%s", sep, $0;sep=","} END{printf ",DNS:localhost"}')
command -v openssl >/dev/null 2>&1 || { echo '请先安装 openssl' >&2; exit 1; }
grant_container_read=false
if [ "$(uname -s)" = Linux ] && [ "$(id -u)" != 0 ]; then
  command -v setfacl >/dev/null 2>&1 || { echo '请先安装 acl（sudo apt-get install acl），以便仅授权容器读取服务器私钥。' >&2; exit 1; }
  grant_container_read=true
fi
mkdir -p "$cert_dir"
if [ -f "$cert_dir/ca.key" ] && [ -f "$cert_dir/ca.crt" ]; then
  echo '沿用现有 CA；已信任此 CA 的设备无需重新安装。'
elif [ -e "$cert_dir/ca.key" ] || [ -e "$cert_dir/ca.crt" ]; then
  echo 'CA 文件不完整，请恢复原有 ca.key 和 ca.crt；不会覆盖现有 CA。' >&2; exit 1
else
  openssl req -x509 -newkey rsa:3072 -sha256 -days 3650 -nodes \
    -keyout "$cert_dir/ca.key" -out "$cert_dir/ca.crt" \
    -subj '/CN=LAN Chat Local CA' \
    -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign'
fi
scratch_dir=$(mktemp -d "$cert_dir/.certgen.XXXXXX")
trap 'rm -rf "$scratch_dir"' EXIT HUP INT TERM
openssl req -newkey rsa:2048 -nodes -sha256 \
  -keyout "$scratch_dir/server.key" -out "$scratch_dir/server.csr" \
  -subj "/CN=$server_ip"
cat > "$scratch_dir/extensions.cnf" <<EOF
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=$subject_alternatives
EOF
openssl x509 -req -in "$scratch_dir/server.csr" -CA "$cert_dir/ca.crt" \
  -CAkey "$cert_dir/ca.key" -set_serial "0x$(openssl rand -hex 16)" \
  -out "$scratch_dir/server.crt" -days 365 -sha256 -extfile "$scratch_dir/extensions.cnf"
openssl verify -CAfile "$cert_dir/ca.crt" "$scratch_dir/server.crt"
mv "$scratch_dir/server.key" "$cert_dir/server.key"
mv "$scratch_dir/server.crt" "$cert_dir/server.crt"
chmod 600 "$cert_dir/ca.key" "$cert_dir/server.key"
if [ "$grant_container_read" = true ]; then
  # Exact-file ACL: UID 0 can read this TLS key without any extra capability.
  # The CA private key and other host files receive no additional permissions.
  setfacl -m u:0:r-- "$cert_dir/server.key"
fi
chmod 644 "$cert_dir/ca.crt" "$cert_dir/server.crt"
echo '完成。当前主机访问地址：'
printf '%s\n' "$host_ips" | while IFS= read -r address; do
  echo "  https://$address:18086"
done
echo "仅将 $cert_dir/ca.crt 分发给使用者安装并信任。不要分发任何 .key 文件。"
echo '服务器证书有效期 365 天；到期前重跑此脚本并重建容器，CA 保持不变。'
