#!/bin/sh
# Read local interface/routing information only; no internet request is made.
set -eu
if command -v ip >/dev/null 2>&1; then
  primary=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="src") {print $(i+1);exit}}' || true)
  addresses=$(ip -o -4 addr show scope global 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="inet") {split($(i+1),a,"/");print a[1]}}' || true)
elif [ "$(uname -s)" = Darwin ]; then
  interface=$(route -n get default 2>/dev/null | awk '/interface:/{print $2;exit}' || true)
  primary=$(ipconfig getifaddr "$interface" 2>/dev/null || true)
  addresses=$(ifconfig 2>/dev/null | awk '$1=="inet"{print $2}' || true)
else
  echo '无法检测主机地址，请安装 iproute2，或给部署脚本传入本机 IPv4 地址。' >&2
  exit 1
fi
# Require an assigned address; ignore loopback, unspecified and link-local IPs.
result=$(printf '%s\n%s\n' "$primary" "$addresses" | awk -F. '
  NF==4 && $1!=0 && $1!=127 && $1<224 && !($1==169 && $2==254) {
    valid=1; for(i=1;i<=4;i++) if($i!~/^[0-9]+$/ || $i>255) valid=0;
    if(valid && !seen[$0]++) print
  }')
if [ -z "$result" ]; then
  echo '未检测到可用的本机 IPv4 地址，请连接局域网后重试，或手动指定 IP。' >&2
  exit 1
fi
printf '%s\n' "$result"
