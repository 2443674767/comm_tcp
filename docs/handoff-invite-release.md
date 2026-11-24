> 已于 2026-11-13 完成：修复会话恢复遗漏，33 项浏览器测试通过，完成打包及服务器升级。原数据和证书已核对保留，两项真实远程测试通过。当前版本 `2026.11.13-invite-amd64`；实际结果和回滚信息见 `docs/remote-deployment-192.168.123.164.md`。下文保留原交接内容供追溯。

# 交接：打包并部署“拉人进语音”版（2026.11.13-invite-amd64）

写给接手的 AI。你的任务是：在开发机（macOS）上验证并打包，然后把发布包升级到 Ubuntu amd64 服务器，最后把结果记录回项目。代码已经写完，不需要再开发功能；如果验证失败，先修到通过再打包。

项目根目录：`/Users/mrzhang/Code/jiangxing/tcp_comm`（下文的相对路径都以它为准）。项目不是 git 仓库，改文件前自己留意备份。

## 1. 当前状态

**已完成**

- 功能代码：`internal/invite/`（新包）、`internal/chat/server.go`、`internal/chat/hub.go`、`web/app.js`、`web/index.html`、`web/style.css`。
- 测试代码：`internal/invite/invite_test.go`、`internal/chat/invite_test.go`、`tests/voice-invite.spec.js`。
- 版本号已统一为 `2026.11.13-invite-amd64`：`scripts/package-ubuntu-amd64.py`、`deploy/ubuntu-amd64/` 下的 `compose.yaml`、`install.sh`、`upgrade.sh`。
- 发布包文件名已定为 `lan-chat-ubuntu-amd64-20261113-invite.tar.gz`，`deploy/ubuntu-amd64/部署说明.md` 和 `升级说明.md` 已同步。
- `deploy/ubuntu-amd64/验证记录.md` 已改为本版，列出了已验证和未验证的内容。

**未完成（就是你的任务）**

- 跨频道邀请加入之后，`go test -race ./...`、`go vet ./...`、`npm test` 都还没有在开发机上跑过。`internal/chat` 的改动和 `internal/chat/invite_test.go` 只做过类型检查，从未真正执行。
- 发布包还没有生成，`dist/` 里最新的仍是上一版 `20261111-voice`。
- 服务器还在运行上一版 `lan-chat:2026.11.11-voice-amd64`。

## 2. 这一版改了什么（排查问题时用）

功能：在群语音卡片点“拉人进语音”，勾选任意在线用户（不限频道）一起邀请。被邀请者弹出来电卡片并循环响铃，可接受或拒绝，30 秒未应答算未接听。接受后自动切换到发起人所在频道并加入其群语音。

实现要点：

- 邀请只存在内存里（`invite.Manager`），不写数据库，**本版没有数据库结构变化**。
- 新接口：`POST /api/voice-invite`（发起，要求发起人已在该频道语音中）、`POST /api/voice-invite/reply`（应答，`action` 只接受 `decline` 或 `failed`）。
- 没有“接受”接口：被邀请者加入语音房间即视为接受，由房间的 `OnChange` 回调调用 `invites.Sync` 结算。发起人退出语音时同一回调取消未应答的邀请。
- 新的 WebSocket 事件：`online`（全体在线名单，带 `channel_id`，广播给所有频道）、`voice_invite`、`voice_invite_end`、`voice_invite_reply`。后三个按用户投递，不分频道。
- 用户失去全部连接后有 5 秒宽限（`inviteOfflineGrace`）才算离线，用来容忍切换频道和短暂重连。
- 前端把原来的加入语音点击处理改成了 `joinVoice()` 函数（成功返回 `true`），邀请和接受都复用它。
- 铃声遵循“提示音”开关；页面未被点击过时浏览器不允许出声，这是浏览器限制，不是缺陷。

## 3. 开始前向用户确认

这几件事不要自己假设：

1. **目标服务器**。记录里的服务器是 `192.168.123.164`，用户 `unitree`，部署目录 `/home/unitree/lan-chat-ubuntu-amd64`（见 `docs/remote-deployment-192.168.123.164.md`）。确认这次仍是这台。
2. **登录方式**。项目里没有保存服务器密码，也不要把密码写进任何文件。向用户要登录方式，或请用户自己执行需要登录的步骤。
3. **停服时间**。升级会中断现有网页连接和通话（网页自动重连，语音要重新加入）。确认现在可以升级。

## 4. 第一步：在开发机上验证

需要 Go 1.24+、Node.js、Docker Desktop（已启动）。在项目根目录执行：

```sh
go test -race ./...
go vet ./...
python3 scripts/deployment_test.py
python3 scripts/upgrade_test.py
npm ci
npx playwright install chromium
npm test
```

全部通过才能进入打包。注意事项：

- `npm test` 会自己用 `go run` 在 18096/TCP、18097/UDP、18095/TCP 起一个测试服务，数据在 `.test-data/`。这几个端口被占用时先找出占用者。
- 开发机上可能还有一个手动启动的 `lanchat` 占着 18086、18087、18085。它不影响 `npm test`，但如果你要手动起服务，用 `lsof -nP -iTCP:18085 -sTCP:LISTEN` 查清楚再处理，别直接杀进程，先问用户。
- `DEV_HTTP=1` 只允许监听回环地址，这是程序有意的限制。要让局域网其他主机访问，必须不带 `DEV_HTTP` 用 HTTPS 启动（`go run ./cmd/lanchat`）。
- `scripts/deployment_test.py` 在没有装 `acl` 的 Linux 上会有 5 项失败（报“请先安装 acl”），那是环境问题；在 macOS 上应当 6 项全过。
- 语音播报相关的浏览器用例要等系统语音引擎，偶尔会慢；单项超时先重跑一次再判断。
- 新增的 `tests/voice-invite.spec.js` 用路由替身代替服务器，10 项，不依赖真实语音。

如果有失败：读失败信息，修代码或修测试，直到上面全部通过。不要为了通过而删用例或放宽断言；确实是用例本身写错时可以改，并在验证记录里写明。

## 5. 第二步：打包

```sh
python3 scripts/package-ubuntu-amd64.py
```

脚本会交叉编译 linux/amd64 静态程序、构建并导出 amd64 镜像、生成包内 `SHA256SUMS`、打出压缩包并自检。成功时最后一行是 `Release verified: lan-chat-ubuntu-amd64-20261113-invite.tar.gz, ... SHA256 ...`。

产物在 `dist/`：

- `lan-chat-ubuntu-amd64-20261113-invite.tar.gz`
- `lan-chat-ubuntu-amd64-20261113-invite.tar.gz.sha256`
- `lan-chat-source.tar.gz`（源码存档，会被覆盖）

核对：

```sh
cd dist && shasum -a 256 -c lan-chat-ubuntu-amd64-20261113-invite.tar.gz.sha256
tar -tzf lan-chat-ubuntu-amd64-20261113-invite.tar.gz | grep -E '\.key$|\.db$|/certs/|/data/' && echo '不应包含这些文件' || echo '无敏感文件'
file lan-chat-ubuntu-amd64/lanchat   # 应为 ELF 64-bit x86-64, statically linked
```

记下 SHA-256，后面要写进部署记录。

可选但建议：按上一版的做法，用最终镜像起一个只读容器跑一遍浏览器用例（上一版的做法记在 `deploy/ubuntu-amd64/验证记录.md` 的“上一版”部分）。做不了就在验证记录里如实写“未做”。

## 6. 第三步：升级服务器

用户确认后进行。把压缩包和 `.sha256` 一起传到服务器，例如：

```sh
scp dist/lan-chat-ubuntu-amd64-20261113-invite.tar.gz dist/lan-chat-ubuntu-amd64-20261113-invite.tar.gz.sha256 unitree@192.168.123.164:~/
```

在服务器上执行。新版必须解压到**独立的新目录**，不能覆盖原部署目录：

```sh
sha256sum -c lan-chat-ubuntu-amd64-20261113-invite.tar.gz.sha256
mkdir -p ~/lan-chat-new-invite
tar -xzf lan-chat-ubuntu-amd64-20261113-invite.tar.gz -C ~/lan-chat-new-invite
cd ~/lan-chat-new-invite/lan-chat-ubuntu-amd64
sh upgrade.sh /home/unitree/lan-chat-ubuntu-amd64
```

`upgrade.sh` 会校验包和镜像架构、给旧镜像打回滚标签、停服、把整个 `/data` 和证书备份到原目录的 `backups/时间戳-进程号/`、换新版、等健康检查通过。成功时打印“升级完成”和备份路径，**记下这个备份路径**。

失败时的行为：备份阶段失败会尝试恢复旧服务；换新版后失败会停服并保留备份。遇到后一种情况不要反复重试，按 `deploy/ubuntu-amd64/升级说明.md` 的“回滚”一节处理，并告诉用户。

**不要做的事**

- 不要执行 `docker compose down -v`，会删掉聊天数据。
- 不要在原部署目录重新运行 `install.sh`，也不要重新生成证书；现有证书要保留，否则所有客户端都得重装 CA。
- 不要改动服务器上其他容器、Docker 版本、代理配置或系统时间。
- 不要把 `ca.key`、`server.key`、数据库或录音从服务器拷出来。

## 7. 第四步：升级后检查

在服务器上：

```sh
cd /home/unitree/lan-chat-ubuntu-amd64
docker compose ps
docker compose exec -T chat /lanchat healthcheck
docker compose logs --tail=50 chat
docker image inspect lan-chat:2026.11.13-invite-amd64 --format '{{.Os}}/{{.Architecture}}'
```

从开发机用服务器的 CA 严格校验 HTTPS（公开证书在项目的 `certs/192.168.123.164-ca.crt`）：

```sh
curl --cacert certs/192.168.123.164-ca.crt https://192.168.123.164:18086/healthz
```

应返回 `{"status":"ok"}`。服务器时钟曾比开发机快约 100 秒，若报证书时间问题先核对两端时间。

功能验收（客户端刷新页面即可拿到新版，不用重装证书）：

1. 旧的公共频道历史和录音仍在。
2. 两个会话各进不同频道，一方点“拉人进语音”勾选另一方：对方响铃并弹出卡片，卡片写明频道。
3. 接受：自动切到发起人的频道并进入通话，双向有声音。
4. 拒绝：留在原频道，发起人看到“拒绝了语音邀请”。
5. 30 秒不操作：卡片消失，发起人看到“未接听语音邀请”。
6. 关闭“提示音”后再被邀请：只弹卡片不响铃。
7. 发起人在对方应答前退出语音：对方卡片消失并提示已取消。

能用浏览器自动化连真实服务器就自动跑，上一版就是这么验的；自动化用的是模拟麦克风，真机的铃声、麦克风和锁屏行为仍需用户在手机上试。测试消息发到单独的测试频道，不要写进公共频道，测试会话用完退出。

## 8. 第五步：把结果记回项目

- `deploy/ubuntu-amd64/验证记录.md`：把“本版尚未完成的验证”里实际做完的项移到“已完成”，写明命令和结果；没做的保留并说明原因。注意这个文件会被打进发布包，如果在打包后修改，包内的是旧内容——要么打包前就把第 4 步的结果写进去，要么接受包内记录略旧并在部署记录里说明。
- `docs/remote-deployment-192.168.123.164.md`：更新“最近升级”日期和镜像版本，新增一节“2026-11-13 升级与验收”，写上发布包 SHA-256、备份目录、回滚镜像标签、做过的验收和没做的验收。
- 只写实际发生的事。没跑的测试写“未跑”，不要照抄上一版的措辞。

## 9. 回滚

本版相对上一版没有数据结构变化。如果只是新功能有问题、数据无异常，可以在原部署目录把 `compose.yaml` 里的镜像改回 `upgrade.sh` 打的回滚标签（记在备份目录的 `image-tag.txt`）后 `docker compose up -d --no-build --pull never --force-recreate`。如果数据也需要回到升级前，按 `deploy/ubuntu-amd64/升级说明.md` 的“回滚”一节把备份恢复到新数据卷。两种情况都先告诉用户再动手。

## 10. 完成后向用户汇报

- 各项测试的结果（通过数、失败及处理）。
- 发布包文件名和 SHA-256。
- 服务器当前镜像版本、健康检查结果、备份目录和回滚标签。
- 验收清单里哪些验过、哪些没验、哪些需要用户用真机确认。
- 过程中改过的任何代码或测试。
