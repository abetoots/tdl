---
title: "下载"
weight: 30
---

# 下载

## 从链接下载：

{{< hint info >}}
点击官方客户端的 "复制链接" 按钮获取消息链接。
{{< /hint >}}

{{< include "snippets/link.md" >}}

{{< command >}}
tdl dl -u https://t.me/tdl/1 -u https://t.me/tdl/2
{{< /command >}}

## 从 JSON 下载：

有两种导出 JSON 文件的方式：

{{< tabs "json" >}}
{{< tab "tdl" >}}
请参考 [导出消息](/zh/guide/tools/export-messages)
{{< /tab >}}

{{< tab "桌面客户端" >}}

1. 选择要导出的对话，点击右上角的三个点，然后点击 `导出聊天历史`。
2. 取消选中所有选项（您现在不需要下载它们），将 `大小限制` 设置为最小值。
3. 设置格式为 `JSON` 并选择您需要的时间段。
4. 导出它！`result.json` 就是您需要的文件。
   {{< /tab >}}
   {{< /tabs >}}

{{< command >}}
tdl dl -f result1.json -f result2.json
{{< /command >}}

## 合并下载：

{{< command >}}
tdl dl \
-u https://t.me/tdl/1 -u https://t.me/tdl/2 \
-f result1.json -f result2.json
{{< /command >}}

## 自定义目录：

将文件下载到自定义目录

{{< command >}}
tdl dl -u https://t.me/tdl/1 -d /path/to/dir
{{< /command >}}

## 自定义参数：

使用每个任务8个线程，4个并发任务下载：

{{< command >}}
tdl dl -u https://t.me/tdl/1 -t 8 -l 4
{{< /command >}}

## 反序下载：

按反序下载文件（从最新到最旧）

{{< hint warning >}}
不同的顺序将影响“恢复下载”功能
{{< /hint >}}

{{< command >}}
tdl dl -f result.json --desc
{{< /command >}}

## MIME 探测：

如果文件扩展名与 MIME 类型不匹配，tdl将使用正确的扩展名重命名文件。

{{< hint warning >}}
副作用：例如 `.apk` 文件将被重命名为 `.zip`。
{{< /hint >}}

{{< command >}}
tdl dl -u https://t.me/tdl/1 --rewrite-ext
{{< /command >}}

## 相册/组合消息探测

自动检测消息是否为相册/组合消息，并下载相应的所有文件。

{{< command >}}
tdl dl -u https://t.me/tdl/1 --group
{{< /command >}}

## 自动跳过

在下载时跳过相同的文件（即名称和大小相同）。

{{< command >}}
tdl dl -u https://t.me/tdl/1 --skip-same
{{< /command >}}

## "Takeout" 会话

通过 ["Takeout" 会话](https://arabic-telethon.readthedocs.io/en/stable/extra/examples/telegram-client.html#exporting-messages) 下载文件：

> 如果下载大量媒体，更倾向于使用 "Takeout" 会话，它允许您以较低的速率限制从您的帐户中导出数据。

{{< command >}}
tdl dl -u https://t.me/tdl/1 --takeout
{{< /command >}}

{{< hint info >}}
如果 Telegram 无法启动 "Takeout" 会话，tdl 会在日志文件中记录 `takeout error`，然后不使用该会话继续下载。
{{< /hint >}}

## 过滤器

使用扩展名过滤器下载文件：

{{< hint warning >}}
扩展名仅与文件名匹配，而不是 MIME 类型。因此，这可能不会按预期工作。

白名单和黑名单不能同时使用。
{{< /hint >}}

白名单：只下载扩展名为 `.jpg` `.png` 的文件

{{< command >}}
tdl dl -u https://t.me/tdl/1 -i jpg,png
{{< /command >}}

黑名单：下载除了扩展名为 `.mp4` `.flv` 的所有文件

{{< command >}}
tdl dl -u https://t.me/tdl/1 -e mp4,flv
{{< /command >}}

## 文件名模板

使用自定义文件名模板下载：

请参考 [模板指南](/zh/guide/template) 了解更多。

{{< command >}}
tdl dl -u https://t.me/tdl/1 \
--template "{{ .DialogID }}_{{ .MessageID }}_{{ .DownloadDate }}_{{ .FileName }}"
{{< /command >}}

## 带话题的群组

将带话题的群组按话题下载到各自的文件夹：先导出整个群组，再用模板字段 `TopicID` 和 `TopicTitle` 命名文件夹。

{{< command >}}
tdl chat export -c CHAT -o group.json
tdl dl -f group.json \
--template "{{ .TopicID }}{{ with .TopicTitle }} {{ filenamify . 60 }}{{ end }}/{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}"
{{< /command >}}

请将 `CHAT` 替换为群组的用户名或 ID。这会创建 `5 Releases`、`1 General` 这样的文件夹。没有话题的聊天中的文件会放进 `0`。两个字段的说明请参考 [模板指南](/zh/guide/template)。

如果只想下载部分话题，请用 `--topic` 逐个导出，再把所有文件交给同一次下载。`tdl chat ls` 会列出每个群组的话题 ID。没有媒体的话题请不要加入：目前，没有任何消息的导出文件会让下载在处理它之后的文件之前就结束。

{{< command >}}
tdl chat export -c CHAT --topic 5 -o topic-5.json
tdl chat export -c CHAT --topic 9 -o topic-9.json
tdl dl -f topic-5.json -f topic-9.json \
--template "{{ .TopicID }}{{ with .TopicTitle }} {{ filenamify . 60 }}{{ end }}/{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}"
{{< /command >}}

## 恢复/重新开始下载

在不需要交互的情况下恢复下载：

{{< command >}}
tdl dl -u https://t.me/tdl/1 --continue
{{< /command >}}

在不需要交互的情况下重新开始下载：

{{< command >}}
tdl dl -u https://t.me/tdl/1 --restart
{{< /command >}}

## 下载失败

下载失败的文件会显示 `failed!` 和原因，其他文件继续下载。结束时 tdl 会报告失败的文件数并以错误退出，下次使用 `--continue` 即可重试这些文件。

- **Telegram 一直拒绝的文件**会失败，而不会卡住整个下载。对于一个文件分片，等待 10 次 `FLOOD_WAIT` 后，或遇到 `RPC_CALL_FAIL` 等 Telegram 内部错误时共尝试 5 次（退避约一分钟）后，tdl 会放弃。错误信息会显示 Telegram 给出的原因。
- **`FLOOD_PREMIUM_WAIT`** 是 Telegram 对非 Premium 账户的限速，tdl 总会等待它结束。
- **无法恢复的连接**算作失败，绝不会被当成下载完成。在此之前，tdl 会先重试该请求；请参阅 [`--reconnect-timeout`](/zh/guide/global-config#--reconnect-timeout)。
- **Ctrl+C** 会停止下载并保存进度。之后 `--continue` 会跳过已完成的文件，并从头重新下载被中断的文件。

## HTTP 文件服务器

将文件暴露为 HTTP 服务器，而不使用内置下载它们

{{< hint info >}}
当您想要使用下载管理器（如 `aria2`/`wget`/`axel`/`IDM`）下载文件时，适合使用此选项。
{{< /hint >}}

{{< command >}}
tdl dl -u https://t.me/tdl/1 --serve
{{< /command >}}

使用自定义端口：

{{< command >}}
tdl dl -u https://t.me/tdl/1 --serve --port 8081
{{< /command >}}
