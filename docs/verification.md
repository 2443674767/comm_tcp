# 验证记录

验证日期：2026-11-09（Asia/Shanghai）。开发机 macOS arm64；Docker Desktop Linux；目标 Ubuntu 主机未连接。

## 已通过

- `go test -race -count=1 ./...`：SQLite 重启/会话过期/幂等/分页，HTTP 与真实 WebSocket，鉴权及来源检查，录音存取、媒体类型校验，零游标补历史，Pion 实际 RTP 转发与槽位复用，人数上限，无效 offer 清理，通话 ID 退出隔离。
- `go vet ./...`。
- `PW_CHANNEL=chrome npm test -- --reporter=line`：6 项通过。两个独立 Chrome 会话（一个 390px 手机宽度），验证文字互收与刷新历史、录音发送及实际解码播放、服务器群语音 RTP 接收、静音/退出/再加入；另含麦克风拒绝、文本注入、重连乱序、跨标签页换昵称、另一页权限等待时关闭、历史 HTTP 延迟期间重连的回归测试。
- Linux amd64 静态二进制交叉编译。
- `docker compose config --quiet` 以及实际 `docker build`。
- 本地 Docker 容器在只读根文件系统和独立数据卷下运行。使用生成的 CA 严格验证 HTTPS；创建昵称会话、文字和实际浏览器录音；重启容器后验证原会话可用、文字/录音记录存在、录音文件字节完全一致。
- 容器内 `/lanchat healthcheck` 通过，Docker 状态 healthy。
- 证书 IP SAN 与 CA 链验证通过；重跑脚本可复用 CA 并续期服务器证书。未更改任何系统信任库。
- 独立代码审查报告的历史同步与多标签页问题均已回归验证，最后一项延迟同步问题经审查者独立复现复核通过。

## 验证范围

手机检查为桌面浏览器的手机尺寸视口，自动测试使用模拟麦克风。仍需在目标 Ubuntu 上部署并使用真实 iOS/Android、Windows/macOS 设备验收 CA 信任、麦克风权限、实际声音及防火墙；不声称已远程部署。ICE-TCP 是浏览器支持时的备用通道，不替代 TURN，也不保证跨任意网络成功；优先放行 UDP。

Docker 构建有一条 TLS_KEY 环境变量名称提示；这里保存的是 `/certs/server.key` 文件路径，不是私钥内容。私钥仅以只读文件挂载，未进入镜像或部署包。

## 后续变更：自动使用部署主机 IP

- `python3 scripts/deployment_test.py`：6 项通过，覆盖自动地址和多网卡 SAN、迁移后 CA 复用、无参数识别、无默认路由的离线局域网、无地址报错、显式覆盖及部署顺序/清除旧 .env IP。
- 当前开发机实际地址读取通过；不发送外网请求。
- `go test -race ./...` 和 `go vet ./...` 通过。
- 浏览器配置移除 VOICE_HOST_IP，6 项端到端测试仍通过，含实际 RTP 转发。
- Docker 配置解析确认 VOICE_HOST_IP 默认空；Docker 镜像重新构建。
- 独立审查通过，真实 Ubuntu 多网卡路由仍需现场验收。
