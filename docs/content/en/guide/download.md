---
title: "Download"
weight: 30
---

# Download

## From Links:

{{< hint info >}}
Get message links from "Copy Link" button in official clients.
{{< /hint >}}

{{< include "snippets/link.md" >}}

{{< command >}}
tdl dl -u https://t.me/tdl/1 -u https://t.me/tdl/2
{{< /command >}}

## From JSON:

There are two ways to export the JSON you need:

{{< tabs "json" >}}
{{< tab "tdl" >}}
This is especially for protected chats and more powerful than the desktop client.

Please refer to [Export Messages](/guide/tools/export-messages)
{{< /tab >}}

{{< tab "Desktop Client" >}}

1. Choose the dialog you want to export, and click the three dots in the upper right corner, then
   click `Export Chat History`.
2. Uncheck all boxes(you don't need to download them now) and set `Size Limit` to minimum
3. Set Format to `JSON` and select the time period you need.
4. Export it! And `result.json` is what you need.
   {{< /tab >}}
   {{< /tabs >}}

{{< command >}}
tdl dl -f result1.json -f result2.json
{{< /command >}}

## Combine Sources:

{{< command >}}
tdl dl \
-u https://t.me/tdl/1 -u https://t.me/tdl/2 \
-f result1.json -f result2.json
{{< /command >}}

## Custom Destination:

Download files to custom directory

{{< command >}}
tdl dl -u https://t.me/tdl/1 -d /path/to/dir
{{< /command >}}

## Custom Parameters:

Download with 8 threads per task, 4 concurrent tasks:

{{< command >}}
tdl dl -u https://t.me/tdl/1 -t 8 -l 4
{{< /command >}}

## Descending Order:

Download files in descending order(from newest to oldest)

{{< hint warning >}}
Different order will affect resuming download
{{< /hint >}}

{{< command >}}
tdl dl -f result.json --desc
{{< /command >}}

## MIME Detection:

If the file extension is not matched with the MIME type, tdl will rename the file with the correct extension.

{{< hint warning >}}
Side effect: like `.apk` file, it will be renamed to `.zip`.
{{< /hint >}}

{{< command >}}
tdl dl -u https://t.me/tdl/1 --rewrite-ext
{{< /command >}}

## Album/Grouped Detection

Automatically detect if the message is an album/grouped message and download all of them.

{{< command >}}
tdl dl -u https://t.me/tdl/1 --group
{{< /command >}}

## Auto Skip

Skip the same files(name and size) when downloading.

{{< command >}}
tdl dl -u https://t.me/tdl/1 --skip-same
{{< /command >}}

## Takeout Session

Download files
with [takeout session](https://arabic-telethon.readthedocs.io/en/stable/extra/examples/telegram-client.html#exporting-messages):

> If you plan to download a lot of media, you may prefer to do this within a takeout session. Takeout sessions let you
> export data from your account with lower flood wait limits.

{{< command >}}
tdl dl -u https://t.me/tdl/1 --takeout
{{< /command >}}

{{< hint info >}}
If Telegram won't start a takeout session, tdl writes `takeout error` to its log file and downloads without one.
{{< /hint >}}

## Filters

Download files with extension filters:

{{< hint warning >}}
The extension is only matched with the file name, not the MIME type. So it may not work as expected.

Whitelist and blacklist can not be used at the same time.
{{< /hint >}}

Whitelist: Only download files with `.jpg` `.png` extension

{{< command >}}
tdl dl -u https://t.me/tdl/1 -i jpg,png
{{< /command >}}

Blacklist: Download all files except `.mp4` `.flv` extension

{{< command >}}
tdl dl -u https://t.me/tdl/1 -e mp4,flv
{{< /command >}}

## Name Template

Download with custom file name template:

Please refer to [Template Guide](/guide/template) for more details.

{{< command >}}
tdl dl -u https://t.me/tdl/1 \
--template "{{ .DialogID }}_{{ .MessageID }}_{{ .DownloadDate }}_{{ .FileName }}"
{{< /command >}}

## Groups with Topics

Download a group with topics into one folder per topic: export the whole group once, then name the folder with the `TopicID` and `TopicTitle` template fields.

{{< command >}}
tdl chat export -c CHAT -o group.json
tdl dl -f group.json \
--template "{{ .TopicID }}{{ with .TopicTitle }} {{ filenamify . 60 }}{{ end }}/{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}"
{{< /command >}}

Replace `CHAT` with the group's username or ID. This creates folders like `5 Releases` and `1 General`. Files from chats without topics go into `0`. See the [Template Guide](/guide/template) for both fields.

To download only some topics, export each one with `--topic`, then pass all the files to one run. `tdl chat ls` lists each group's topic IDs. Leave out the export of a topic with no media: an export with no messages currently ends the run before the files after it.

{{< command >}}
tdl chat export -c CHAT --topic 5 -o topic-5.json
tdl chat export -c CHAT --topic 9 -o topic-9.json
tdl dl -f topic-5.json -f topic-9.json \
--template "{{ .TopicID }}{{ with .TopicTitle }} {{ filenamify . 60 }}{{ end }}/{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}"
{{< /command >}}

## Resume/Restart

Resume without UI interaction:

{{< command >}}
tdl dl -u https://t.me/tdl/1 --continue
{{< /command >}}

Restart without UI interaction:

{{< command >}}
tdl dl -u https://t.me/tdl/1 --restart
{{< /command >}}

## Failed Downloads

A file that fails shows `failed!` with the reason, and the other files keep downloading. At the end, tdl reports how many files failed and exits with an error, so `--continue` retries them next time.

- **A file Telegram keeps refusing** fails instead of stalling the run. tdl gives up on a file part after waiting out 10 `FLOOD_WAIT`s, or after 5 attempts in total, with about a minute of backoff, for Telegram internal errors such as `RPC_CALL_FAIL`. The error shows Telegram's reason.
- **`FLOOD_PREMIUM_WAIT`**, Telegram's speed limit for accounts without Premium, is always waited out.
- **A connection that can't be recovered** counts as a failure, never as a finished run. tdl retries the request first; see [`--reconnect-timeout`](/guide/global-config#--reconnect-timeout).
- **Ctrl+C** stops the run and saves progress. `--continue` then skips the files that finished, and downloads interrupted files again from the start.

## Serve

Expose the files as an HTTP server instead of downloading them with built-in downloader

{{< hint info >}}
This is useful when you want to download files with a download manager like `aria2`/`wget`/`axel`/`IDM`...
{{< /hint >}}

{{< command >}}
tdl dl -u https://t.me/tdl/1 --serve
{{< /command >}}

With custom port:

{{< command >}}
tdl dl -u https://t.me/tdl/1 --serve --port 8081
{{< /command >}}
