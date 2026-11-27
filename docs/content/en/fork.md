---
title: "About This Fork"
weight: 5
---

# About This Fork

This site documents [abetoots/tdl](https://github.com/abetoots/tdl), a fork of [iyear/tdl](https://github.com/iyear/tdl) by [iyear](https://github.com/iyear). It adds fixes and features that upstream doesn't have yet. Everything else works as upstream documents it.

Every change against upstream: [compare iyear/tdl with abetoots/tdl](https://github.com/iyear/tdl/compare/master...abetoots:tdl:master).

## What's Different

### Downloads end on their own and report failures

From [#3](https://github.com/abetoots/tdl/pull/3):

- A failed file is reported as failed, and the run exits with an error, so `--continue` retries it. See [Failed Downloads](/guide/download#failed-downloads).
- A file Telegram keeps refusing fails with Telegram's reason, instead of stalling the run.
- A lost connection never ends a command as if it had succeeded.
- If a takeout session can't start, downloads go on without it instead of hanging.
- `tdl forward` reports an album it can't look up, instead of skipping it silently.

### Newer Telegram library

From [#4](https://github.com/abetoots/tdl/pull/4):

- gotd/td v0.166.1 (Telegram API layer 233). When Telegram drops a connection's state (`CONNECTION_LAYER_INVALID`), gotd re-sends the connection setup (`initConnection`) and retries the request, instead of the run aborting.
- The session data gains new fields. Back up your `.tdl` folder before switching between this fork and upstream.

### Topic folders

From [#6](https://github.com/abetoots/tdl/pull/6):

- The `TopicID` and `TopicTitle` [template fields](/guide/template) download a group with topics into one folder per topic. See [Groups with Topics](/guide/download#groups-with-topics).

## Installing

This fork doesn't publish its own releases yet. The [installation](/getting-started/installation) instructions still install upstream's builds, which don't include these changes. To use the fork, build it from source with Go 1.25.8 or newer. Build a 64-bit binary: 32-bit builds crash because of a gotd bug. On Windows, name the output `tdl.exe`:

{{< command >}}
git clone https://github.com/abetoots/tdl.git
cd tdl
go build -o tdl .
{{< /command >}}

Run it by its path, `./tdl` (`.\tdl.exe` on Windows), so that an upstream `tdl` on your `PATH` doesn't run instead.

{{< hint warning >}}
Don't run `tdl update` on a fork build: it replaces it with upstream's latest release.
{{< /hint >}}
