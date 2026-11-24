# 视频照片发布 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 手机可视频通话、拍照和发送照片，并清空已授权测试数据。
**Architecture:** 现有 Go/Pion SFU 增加视频，照片复用消息存储与频道鉴权；原端口与证书保持。
**Tech Stack:** Go、Pion、SQLite、原生 JS、Playwright、Docker amd64。
**Spec:** docs/superpowers/specs/2026-11-13-video-photo-design.md

## Global Constraints
- 最多 5 人；摄像头主动开启，手机前后切换，退出释放媒体。
- 证书、部署配置保留；仅删除本项目已授权测试数据和备份。
- 照片上限 10 MiB、20MP，登录读取，频道历史隔离。

## Review Focus
- 摄像头拒绝权限/切换失败后语音正常；离开时资源释放。
- 视频槽位重用/后加入关键帧与正确身份；跨频道不泄漏。
- 图片伪装 MIME、解码炸弹、重试重复文件和路径穿越。
- 手机拍照/选图/大图布局与页面刷新。
- 清理目标必须精确确认，不删除证书配置或其他服务数据。

## Tasks
- [x] 1. 视频：internal/voice、web/app.js/index.html/style.css；先写转发和浏览器失败测试，再实现视频协商、关键帧、前后摄像头、视频网格，运行 Go/PW 测试。
- [x] 2. 照片：internal/chat/photo.go、store.go/server.go、web/photos.js；先写上传校验/鉴权/持久化失败测试，再实现受限上传、预览压缩与查看，集成页面控件并跑手机浏览器用例。
- [x] 3. 独立审查两项改动，修复后完整 race/vet/浏览器测试，记录通过数及未覆盖真机条件。
- [x] 4. 打 linux/amd64 包，远程升级保留证书配置，测试真实 HTTPS 照片和双向视频解码。
- [x] 5. 停服清空本项目运行数据及备份，启动验证仅公共频道且无媒体消息会话；清理本机测试数据；记录发布与清理结果。

## Ledger
- User approved scope and execution. No git repository; source snapshot in .work/video-release for review, not a runtime data backup.

- Completed: video/photo implementation, independent reviews and fixes,37 browser tests, Go race/vet, remote4 video/photo tests and restart persistence, release build/deploy, remote+local authorized reset. Certificates/config retained. Zero messages/sessions/media, only public channel confirmed. Runtime backups removed by authorization; source snapshot/review scratch removed after completion.
