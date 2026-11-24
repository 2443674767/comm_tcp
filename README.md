# 邻语 · 局域网聊天室

昵称登录的多频道聊天室，支持文字、照片、录音和每频道最多 5 人实时语音/视频通话。手机、Windows、macOS 通过浏览器访问；Ubuntu 上用 Docker Compose 部署。默认自动使用部署主机的 IPv4 地址，通过 **`https://<当前主机IP>:18086`** 访问，无需修改代码或填写固定 IP。

## 功能

- 无密码昵称登录，同名用户以独立会话区分；昵称不是经过验证的身份。
- 任意登录用户可创建、命名和选择频道；每频道的文字、录音历史、在线名单、群语音独立，所有用户均可进入任意频道。SQLite 与音频文件持久保存，重启后保留；旧版历史自动迁至公共频道。
- 当前频道收到其他人的新消息时提示；有人真正加入／退出群语音时，当前频道所有在线用户均提示（包括未加入语音者）。加入时先播报“昵称加入语音”，再响两声；退出时先播报“昵称退出语音”，再响一声。连续事件排队播放，关闭提示音、切换频道或退出页面会取消未完成播报。历史补齐和名册更新不提示。
- 昵称播报使用设备的本地中文语音引擎，不向云端发送昵称；需要浏览器提供本地中文声音。声音缺失或播报失败时保留对应的两声／一声提示，并在提示音开关旁显示“中文播报不可用”。每个浏览器提供提示音开关，首次交互启用声音。
- 历史每次加载 50 条，可向上查看全部记录；断线自动重连、补齐消息；发送重试去重。
- 录音支持试听、取消、发送，最长 120 秒、最大 10 MiB。现代浏览器自动选择 WebM/Opus、Ogg/Opus 或 MP4/AAC；接收端的解码兼容性取决于浏览器版本，建议使用最新 Chrome/Edge、Safari、Firefox。
- 所有人可加入/退出所在频道的群语音、静音；每频道最多 5 人，常用 2 人。音频由服务器转发，参与者之间无需直接互通。群语音不录制。
- 拉人进语音：点击群语音卡片的“拉人进语音”，勾选任意在线伙伴（不限频道，可多选、全选）后一起邀请；发起人未在语音中时会先自动加入。被邀请者无论在哪个频道都会看到来电卡片并听到循环铃声，可“接受并加入”或“拒绝”；接受后自动切换到发起人所在频道并加入其群语音（切换频道会清空未发送的输入内容），30 秒未应答算未接听；发起人会看到对方接受、拒绝、未接听或离线的提示。发起人退出语音时未应答的邀请自动取消。铃声遵循提示音开关：关闭时只显示卡片不响铃；与其他提示音一样，需在页面上有过一次点击后才能出声。邀请只存在于内存，不写入聊天记录。
- 录音和群语音不能同时占用麦克风；页面关闭或断线后退出通话，重连后手动加入。
- 中文响应式页面、在线用户列表。手机布局优先聊天和语音控制，在线用户列表在桌面侧栏展示。

新版离线包升级与回滚见 [升级说明](deploy/ubuntu-amd64/升级说明.md)。

## 照片与视频（手机可用）

- 聊天输入区的“照片”从相册选图，“拍照”调用手机拍摄入口；预览后再发送，可取消。浏览器能解码的图片会缩至最长边 1920 并转成 JPEG，原图上限 25 MiB；服务器只接受有效 JPEG/PNG，最大 10 MiB、2000 万像素，并去除元数据。不能解码的 HEIC 等格式请转为 JPEG 后发送。
- 照片显示在当前频道历史，可点开大图，重启后保留。媒体文件沿用数据卷内 `audio/` 目录，包含录音及照片，不能只备份数据库。
- 加入群语音后可主动开启摄像头，与同频道最多 5 人视频通话；不打开摄像头也能听声音、看对方。支持关闭摄像头和手机前后切换，视频通话不录制。
- 摄像头需受信任 HTTPS 和浏览器授权；建议 Android 使用 Chrome、iPhone 使用 Safari。需要保持网页前台，系统后台/锁屏行为不能保证持续通话。

## 在 Ubuntu 部署

需要 Docker Engine、Docker Compose v2、OpenSSL、iproute2；Linux 非 root 用户部署还需 `acl`（提供 setfacl）。将整个项目复制到 Ubuntu 的一个固定目录，然后在该目录执行：

```sh
cp .env.example .env
sh scripts/deploy.sh
docker compose ps
docker compose logs --tail=80 chat
```

部署脚本自动识别当前主机的 IPv4 地址，为所有检测到的本机地址生成 HTTPS 证书，然后构建并启动 Docker。终端会打印访问地址，例如主机 IP 为 `10.0.0.20` 时使用 `https://10.0.0.20:18086`。服务监听所有接口的 18086 端口；群语音默认使用当前主机实际网卡地址，不固定映射到某个旧 IP。

多网卡时，默认路由地址优先显示，其余地址也会列出；请选择客户端能够访问的局域网地址。检测只读取本地路由/网卡信息，不需要访问互联网。没有默认路由时会使用网卡上的地址；没有可用 IPv4 时明确报错，不生成指向其他主机的证书。

如需指定某个地址，可运行 `sh scripts/deploy.sh 10.0.0.20`，或者 `SERVER_IP=10.0.0.20 sh scripts/deploy.sh`。无参数且未设置环境变量时自动识别，并覆盖旧版 `.env` 内遗留的固定 IP；`.env` 仍可配置 GOPROXY。不要在不同机器间复用固定 IP 参数。

**迁移主机或主机 IP 变化后，重新运行 `sh scripts/deploy.sh`**，它会更新证书并重建容器。保留原有 `certs/ca.crt` 和 `certs/ca.key` 时，客户端无需重新安装 CA。单独执行 `docker compose restart` 不会更新证书。

若手动分步部署，执行 `sh scripts/generate-certs.sh`（无需 IP）后再执行 `docker compose up -d --build --force-recreate`；应先清空旧 `.env` 的 `SERVER_IP`。必须先生成证书再启动，避免缺失的文件挂载被 Docker 创建成目录。

Linux 非 root 用户生成证书时，脚本仅通过文件 ACL 授予容器 UID 0 对 `server.key` 的读取权限；CA 私钥不授权、不挂载，容器仍移除全部 capabilities。可用 `getfacl certs/server.key` 查看实际权限。

Docker Compose 使用 **host 网络**，面向 Ubuntu/Linux 服务器，不要求 Windows/Mac 客户端安装 Docker。容器不依赖公网服务、外部 CDN、STUN 或 TURN；首次构建需要下载 Go 镜像及依赖。GOPROXY 可通过 `.env` 修改。

### 放行端口

| 端口 | 用途 |
|---|---|
| 18086/TCP | HTTPS 页面、聊天、录音及语音协商 |
| 18087/UDP | WebRTC 群语音主通道 |
| 18085/TCP | ICE-TCP 语音备用通道 |

如果 Ubuntu 已启用 UFW，根据访问范围添加规则，例如：

```sh
sudo ufw allow 18086/tcp
sudo ufw allow 18087/udp
sudo ufw allow 18085/tcp
```

聊天仅需 18086/TCP 可达；群语音还需媒体通道可达。ICE-TCP 并非 TURN，浏览器/网络不支持时不能保证 TCP 回退，优先确保 18087/UDP 畅通。跨网段使用时，客户端必须能路由到部署主机的 IP，且路由器、VPN、防火墙都放行相应协议；此私有 IP 不会自动变成公网地址。

### 首次安装证书（每台客户端一次）

将服务器上的 **`certs/ca.crt`** 通过可信方式复制到客户端。**只分发这个公开证书，不分发 `ca.key` 或 `server.key`。** HTTPS 麦克风权限要求证书受信任，仅跳过浏览器证书警告不能作为可靠的语音方案。

- **iPhone/iPad**：将 ca.crt 保存到设备并打开，按提示安装描述文件（设置 → 通用 → VPN 与设备管理）；随后到设置 → 通用 → 关于本机 → 证书信任设置，为 `LAN Chat Local CA` 开启完全信任。用 Safari 打开地址，并允许麦克风。
- **Android**：设置中搜索“安装证书”或“CA 证书”，选择 ca.crt 并按系统提示安装；不同厂商路径不同。用 Chrome 打开地址并允许麦克风。
- **Windows**：双击 ca.crt → 安装证书 → 当前用户 → 手动选择“受信任的根证书颁发机构”。关闭并重新打开浏览器。
- **macOS**：用“钥匙串访问”将 ca.crt 导入登录或系统钥匙串，打开该证书，将 SSL 信任设为“始终信任”，然后重启浏览器。
- **Firefox**：如未自动信任系统证书，在设置 → 隐私与安全 → 证书 → 查看证书 → 证书颁发机构中导入 ca.crt 并信任用于识别网站。

安装后访问 **`https://<当前主机IP>:18086`**，确认无证书警告，再输入昵称。请在系统浏览器中使用；微信等应用内浏览器可能不支持完整麦克风/WebRTC 功能。首次接收语音若被自动播放策略阻止，点击“开启通话声音”。手机切到后台或锁屏后，系统可能暂停网页，持续通话请保持页面在前台。

CA 有效期 10 年，服务器证书有效期 365 天。提前执行 `sh scripts/deploy.sh` 续期并重建容器；脚本复用原有 CA，因此客户端无需重新信任。不要删除原 CA 文件。

## 数据、备份与更新

`chat-data` Docker 命名卷保存 `/data/chat.db`、WAL 文件及 `/data/audio`。证书独立保存在项目的 `certs/`，CA 私钥不会挂进运行容器。`docker compose restart`、`down`、重新构建都保留数据；**`docker compose down -v` 会删除聊天数据**。

一致性备份：停服后复制整个 `/data` 目录，同时单独备份 `certs/`。例如：

```sh
docker compose stop chat
mkdir -p backup
docker cp "$(docker compose ps -aq chat)":/data ./backup/data
cp -a certs ./backup/certs
docker compose start chat
```

备份文件中包含私钥与会话令牌，应放在仅管理员可访问的位置。恢复时将备份的整个 data 目录内容复制回停止状态容器的 `/data`，并恢复 certs。更新代码后执行 `sh scripts/deploy.sh`。

## 开发与验证

运行需要 Go 1.24+；Node.js 仅用于浏览器自动测试，不是部署依赖。

```sh
go test ./...
go test -race ./...
go vet ./...
python3 scripts/deployment_test.py
python3 scripts/upgrade_test.py
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/lanchat-linux-amd64 ./cmd/lanchat

# 本机开发：明文模式只允许监听回环 IP，其他设备无法通过该模式访问。
DEV_HTTP=1 LISTEN_ADDR=127.0.0.1:18086 go run ./cmd/lanchat

# 真实 Chromium 会话，使用浏览器自带模拟麦克风，包含 RTP 接收检查。
npm ci
npx playwright install chromium
npm test
```

浏览器测试使用独立的 `.test-data/`，网页 18096、媒体 18097/UDP 与 18095/TCP；测试数据不会覆盖正式的 `data/` 或 Docker 卷。截图保存在 `test-results/desktop.png` 和 `test-results/mobile.png`。

环境变量：`LISTEN_ADDR`、`VOICE_HOST_IP`（默认留空，自动使用本机地址）、`VOICE_UDP_ADDR`、`VOICE_TCP_ADDR`（留空可禁用 TCP）、`DATA_DIR`、`TLS_CERT`、`TLS_KEY`、`TLS_CA`。`DEV_HTTP=1` 仅用于回环开发。健康检查：容器执行 `/lanchat healthcheck`，会用 CA 验证本机 HTTPS，而不是跳过 TLS 校验。

## 现场验收

1. 两台设备安装 CA，均无证书警告，输入不同昵称登录。
2. 双向发送文字、录音，另一端可收到并播放；退出登录再登录仍有历史。
3. 两端加入群语音，双向讲话，检查静音、退出、重新加入；使用耳机可减少回声。
4. 一端点“拉人进语音”勾选另一端并邀请（另一端先切到别的频道）：对方响铃并弹出卡片，接受后自动切换频道并进入通话；分别验证接受后进入通话、拒绝后发起人收到提示、30 秒不操作提示未接听、关闭提示音后只弹卡片不响铃。
5. 重启容器，确认文字与录音仍可读取；断开一端网络后恢复，确认漏掉的消息补齐。
6. 最多 5 人加入测试，确认第 6 人得到人数已满提示。

自动测试不能替代真实 iOS/Android 麦克风、证书信任和目标 Ubuntu 网络的现场验证。项目不会自动连接或修改目标服务器。
