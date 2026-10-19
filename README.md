# Kstack

_Kstack is a desktop app for monitoring and troubleshooting K8s clusters with help of AI_

<a href="https://www.loom.com/share/41ea1133c96b4dcebc10600ec75d8277">
  <img width="350" alt="video-thumbnail" src="https://github.com/user-attachments/assets/6d7f056f-fe8f-40d7-98f4-c345eb77d0aa" />
</a>

<a href="https://discord.gg/CmsmWAVkvX"><img src="https://img.shields.io/discord/1212031524216770650?logo=Discord&style=flat-square&logoColor=FFFFFF&labelColor=5B65F0&label=Discord&color=64B73A"></a>
[![Slack](https://img.shields.io/badge/Slack-kubetail-364954?logo=slack&labelColor=4D1C51)](https://kubernetes.slack.com/archives/C08SHG1GR37)
[![Contributor Covenant](https://img.shields.io/badge/Contributor%20Covenant-2.1-4baaaa.svg)](CODE_OF_CONDUCT.md)
[![Contributor Resources](https://img.shields.io/badge/Contributor%20Resources-purple?style=flat-square)](https://github.com/kstackhq)

## Introduction

<img width="150" alt="kstack" src="https://github.com/user-attachments/assets/8540a80b-cb5f-42e6-8176-f41983b44676" />

<br>
<br>

**Kstack** is a desktop app that provides a secure command center for monitoring, troubleshooting and auditing your K8s clusters with the help of AI agents. With Kstack, you can use AI agents running in multiple tabs to manage clusters, view logs and debug issues in a sandboxed environment that redacts secrets and withholds credentials from the AI models. Kstack supports all popular AI providers including local models and gives you full control over what the agents can do on your cluster. 

> [!IMPORTANT]
> Kstack is currently under development and this is a developer-only preview of the app.

# Development

## Requirements

- [Rust](https://rustup.rs/) 1.98.1
- [Go](https://go.dev/dl/) 1.26
- [Node](https://nodejs.org/en/download) 24
- [pnpm](https://pnpm.io/installation) 12.4.1 (`npm install -g pnpm@12.4.1`)

On Linux:

```console
sudo apt-get install -y build-essential libwebkit2gtk-4.1-dev \
  libayatana-appindicator3-dev librsvg2-dev
```

For the agent's command sandbox and its tests:

```console
sudo apt-get install -y bubblewrap passt
```

## Run

```console
pnpm tauri dev
```

## Commands

```console
make test-changed  # tests touched by your branch
make test          # all tests
make lint
make vet
make proto         # after editing proto/
make clean-data    # wipe the dev app's data
```

## Read next

- `CLAUDE.md` (root and per area) — conventions
- `docs/adr/` — design decisions
- `docs/security-model.md`
