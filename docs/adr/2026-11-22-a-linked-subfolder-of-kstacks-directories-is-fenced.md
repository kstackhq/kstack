---
title: Fence a linked subfolder of Kstack's directories
date: 2026-11-22
scope: sidecar
status: Superseded by [2026-11-24-follow-a-link-to-a-kstack-folder-once-at-start-and-refuse-one-below-it](2026-11-24-follow-a-link-to-a-kstack-folder-once-at-start-and-refuse-one-below-it.md)
---

# Fence a linked subfolder of Kstack's directories

## Context

The sidecar fences Kstack's directories by the paths the host hands it: the data, cache and runtime
directories, and the host's log, config and state directories. Each fence resolves the paths it is
given, so a linked directory, such as a dotfiles manager's `~/.config/kstack`, is fenced where it
leads. A link one level down was not covered. If `<cache>/kube` or `<data>/chats` led elsewhere, the
files there sat outside the fence, and a folder grant covering the target exposed them to a
sandboxed run and the file tools.

Only the user can plant such a link, since the directories are owner-only. Released builds exist,
so a user may already have done it.

## Decision

`pathsOf` (`sidecar/internal/app/paths.go`) names each subfolder through one helper, `sub`, which
adds it to `bash.Paths.DeniedDirs` when it is a symlink or a Windows junction at start, or when it
cannot be inspected for any reason but absence, since it may be a link. Every fence (the sandbox's
policy, the file tools' `Fence`, the folder grant check) resolves that path when it compares, so
the link's target is fenced. A subfolder that is a folder adds nothing, so Settings' *Never
readable* list does not grow.

The host's own subfolders need nothing more: it hands the sidecar its log directory as a path of its
own, and it refuses a linked `<state>/webview` (`paths.rs` `webview_dir`).

## Alternatives considered

**Refuse a linked subfolder, failing the start with a message to link the whole directory
instead.** It keeps the layout simple, but it stops the app for anyone who already linked one, and
the link does no harm once fenced.

**Fence every subfolder, linked or not.** It would also cover a link planted while Kstack runs, but
it lists eight more folders under *Never readable*, each inside one already listed, and has every
fence make and compare them on every check.

## Consequences

A link made at a subfolder while Kstack runs is not fenced until the next start. A link retargeted
or removed while Kstack runs is fenced where it leads now, as a linked root is: the files at its
former target are not fenced, though a database opened before the change still writes there until
the next start. The start replaces a linked `<runtime>/shell` with a folder (`bash.New` clears it),
so files left at that link's target are not fenced either. A link deeper down, under a chat's or a
monitor's folder, is `rootdir`'s to refuse. A new subfolder in `paths` is named through `sub`.
