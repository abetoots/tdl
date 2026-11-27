---
title: "关于此分支"
weight: 5
---

# 关于此分支

本站是 [abetoots/tdl](https://github.com/abetoots/tdl) 的文档。它是 [iyear](https://github.com/iyear) 的 [iyear/tdl](https://github.com/iyear/tdl) 的一个分支，包含上游尚未提供的修复和功能。其余部分与上游文档一致。

与上游的全部差异：[对比 iyear/tdl 与 abetoots/tdl](https://github.com/iyear/tdl/compare/master...abetoots:tdl:master)。

## 有哪些不同

### 下载会自行结束并报告失败

来自 [#3](https://github.com/abetoots/tdl/pull/3)：

- 下载失败的文件会被报告为失败，并且以错误退出，因此 `--continue` 会重试它。请参阅 [下载失败](/zh/guide/download#下载失败)。
- Telegram 一直拒绝的文件会带着 Telegram 给出的原因失败，而不会卡住整个下载。
- 连接断开绝不会让命令像成功一样结束。
- 如果 "Takeout" 会话无法启动，下载会不使用它继续，而不会卡住。
- `tdl forward` 无法查找相册时会报告出来，而不是静默跳过。

### 更新的 Telegram 库

来自 [#4](https://github.com/abetoots/tdl/pull/4)：

- gotd/td v0.166.1（Telegram API 层 233）。当 Telegram 丢弃连接状态（`CONNECTION_LAYER_INVALID`）时，gotd 会重新发送连接初始化（`initConnection`）并重试该请求，而不会中止下载。
- 会话数据新增了字段。在此分支与上游之间切换前，请先备份 `.tdl` 文件夹。

### 话题文件夹

来自 [#6](https://github.com/abetoots/tdl/pull/6)：

- [模板字段](/zh/guide/template) `TopicID` 和 `TopicTitle` 可以把带话题的群组按话题下载到各自的文件夹。请参阅 [带话题的群组](/zh/guide/download#带话题的群组)。

## 安装

此分支尚未发布自己的版本。[安装](/zh/getting-started/installation) 页面安装的仍是上游的版本，不包含这些改动。要使用此分支，请用 Go 1.25.8 或更新版本从源码构建，并构建 64 位程序：32 位程序会因 gotd 的一个 bug 而崩溃。在 Windows 上请将输出命名为 `tdl.exe`：

{{< command >}}
git clone https://github.com/abetoots/tdl.git
cd tdl
go build -o tdl .
{{< /command >}}

请通过路径运行它：`./tdl`（Windows 上为 `.\tdl.exe`），以免运行 `PATH` 中上游的 `tdl`。

{{< hint warning >}}
不要在此分支的构建上运行 `tdl update`：它会用上游的最新版本替换它。
{{< /hint >}}
