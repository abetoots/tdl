---
title: "模板指南"
bookHidden: true
bookToC: false
---

# 模板指南

本指南将介绍可用于 tdl 模板中的变量和函数。

模板语法基于[Go text/template](https://golang.org/pkg/text/template/)。

## 下载

### 变量 (Beta)

|       变量       |                            描述                             |
|:--------------:|:---------------------------------------------------------:|
|   `DialogID`   |                       Telegram 对话ID                       |
|   `TopicID`    |              话题 ID（常规话题为 `1`，启用话题的群组之外为 `0`）              |
|  `TopicTitle`  | 仅适用于启用话题的群组。标题未经处理，请始终使用 `filenamify`。每次运行时，每个群组获取一次话题列表。 |
|  `MessageID`   |                       Telegram 消息ID                       |
| `MessageDate`  |                    Telegram 消息日期（时间戳）                     |
|   `FileName`   |                       Telegram 文件名                        |
| `FileCaption`  |                   Telegram 文件说明，也就是文本消息                   |
|   `FileSize`   |                     可读的文件大小，例如 `1GB`                      |
| `DownloadDate` |                         下载日期（时间戳）                         |

### 函数 (Beta)

|      函数      |                                             描述                                             |                              用法                              |                                          示例                                           |
|:------------:|:------------------------------------------------------------------------------------------:|:------------------------------------------------------------:|:-------------------------------------------------------------------------------------:|
|   `repeat`   |                                     重复 `STRING` `N` 次                                      |                      `repeat STRING N`                       |                                `{{ repeat "test" 3 }}`                                |
|  `replace`   |                                  对 `STRING` 执行 `PAIRS` 替换                                  |                  `replace STRING PAIRS...`                   |                        `{{ replace "Test" "t" "T" "e" "E" }}`                         |
|   `upper`    |                                      将 `STRING` 转换为大写                                      |                        `upper STRING`                        |                                 `{{ upper "Test" }}`                                  |
|   `lower`    |                                      将 `STRING` 转换为小写                                      |                        `lower STRING`                        |                                 `{{ lower "Test" }}`                                  |
| `snakecase`  |                                 将 `STRING` 转换为 snake_case                                  |                      `snakecase STRING`                      |                               `{{ snakecase "Test" }}`                                |
| `camelcase`  |                                  将 `STRING` 转换为 camelCase                                  |                      `camelcase STRING`                      |                               `{{ camelcase "Test" }}`                                |
| `kebabcase`  |                                 将 `STRING` 转换为 kebab-case                                  |                      `kebabcase STRING`                      |                               `{{ kebabcase "Test" }}`                                |
|    `rand`    |                                  在范围 `MIN` 到 `MAX` 生成随机数                                   |                        `rand MIN MAX`                        |                                   `{{ rand 1 10 }}`                                   |
|    `now`     |                                          获取当前时间戳                                           |                            `now`                             |                                      `{{ now }}`                                      |
| `formatDate` | [格式化](https://zhuanlan.zhihu.com/p/145009400) `TIMESTAMP` 时间戳<br/>(默认格式: `20060102150405`) | `formatDate TIMESTAMP` <br/> `formatDate TIMESTAMP "format"` | `{{ formatDate 1600000000 }}`<br/> `{{ formatDate 1600000000 "2006-01-02-15-04-05"}}` |
| `filenamify` |            尽可能将 `STRING` 转换为合法文件名，可选 `MaxLength` 限制字符串长度避免文件系统限制   |                   `filenamify STRING MaxLength`                 |                             `{{ filenamify .FileName 32 }}`                             |

### 示例：

```gotemplate
{{ .DialogID }}_{{ .MessageID }}_{{ replace .FileCaption `/` `_` `\` `_` `:` `_` }}

{{ .FileName }}_{{ formatDate .DownloadDate }}_{{ .FileSize }}

{{ .FileName }}_{{ formatDate .DownloadDate "2006-01-02-15-04-05"}}_{{ .FileSize }}

{{ lower (replace .FileName ` ` `_`) }}

{{ formatDate (now) }}
```

以下命令将启用话题的群组中的文件按话题下载到各自的文件夹。

```sh
tdl dl -f whole-group.json --template '{{ .TopicID }}/{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}'
```

也可以在文件夹名称中加入话题标题。对于启用话题的群组以外的聊天，此示例使用 `0/`。将标题限制为 60 个字符，即使含有多字节字符，文件夹名称也不会超过 255 字节。

```sh
tdl dl -f whole-group.json --template '{{ .TopicID }}{{ with .TopicTitle }} {{ filenamify . 60 }}{{ end }}/{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}'
```

重命名话题会改变文件夹名称。之后使用 `--skip-same` 下载时，将找不到保存在旧文件夹中的文件。使用 `--continue` 继续下载时，该话题中尚未下载完成的文件会保存到新文件夹。若要保持文件夹名称稳定，请仅使用 `{{ .TopicID }}`。

### 默认：

```gotemplate
{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}
```
