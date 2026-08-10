---
title: main 与 local 分支合并流程
description: 标准版 main 持续合入二开版 local 的操作约定
---

# main 与 local 分支合并流程

## 目标

本项目使用两条长期分支：

- `main`：标准产品分支，只跟踪上游标准版，不直接提交二开功能。
- `local`：二开分支，保存本地定制功能；标准版更新时，把 `main` 合入 `local`。

合并方向固定为：

```text
origin/main -> local -> fork/local
```

不要把 `local` 合并回 `main`，也不要在 `main` 上直接开发二开功能。

## 远端约定

当前仓库使用以下远端：

```text
origin  https://github.com/tigerowo/infinite-canvas.git
fork    git@github.com:liaopj901/infinite-canas.git
```

- `origin` 是标准版仓库，用于拉取最新 `main`。
- `fork` 是二开仓库，用于推送 `local`。
- 如果 `git remote -v` 显示的协议和预期不一致，先检查本机 Git 的 `url.*.insteadOf` 重写配置以及 SSH/HTTPS 认证是否可用。

首次配置远端：

```bash
git remote add fork git@github.com:liaopj901/infinite-canas.git
git ls-remote fork
```

## 每次同步标准版

### 1. 检查工作区

合并前必须确认当前没有未保存的二开代码：

```bash
git switch local
git status --short
```

如果有业务改动，先将它们提交为独立提交，或者由开发者明确保存到其他分支。不要直接丢弃，也不要用 `git reset --hard` 清理工作区。

生成目录、截图和临时文件不要混入业务提交。

### 2. 更新标准版 main

```bash
git fetch origin main
git switch main
git merge --ff-only origin/main
```

`main` 只能快进到 `origin/main`。如果这里无法快进，说明本地 `main` 被额外提交过，应先检查提交来源，不要强行覆盖。

### 3. 合并到 local

```bash
git switch local
git merge --no-ff origin/main -m "Merge origin/main into local"
```

如果没有冲突，检查状态后完成即可：

```bash
git status --short
git log --oneline --decorate --graph -12
```

### 4. 处理冲突

先列出冲突文件：

```bash
git status
git diff --name-only --diff-filter=U
```

处理原则：

1. 保留二开功能的业务意图，不使用整文件覆盖。
2. 接入标准版新增的接口、字段、模型和公共能力。
3. 对同一段代码，优先重写成一份同时满足两边需求的实现。
4. 处理完后确认文件中没有 `<<<<<<<`、`=======`、`>>>>>>>`。
5. 逐个暂存已解决文件并完成合并提交：

```bash
git add -- path/to/resolved-file
git diff --cached --check
git commit --no-edit
```

如果冲突处理方向错误，合并提交前可以取消本次合并：

```bash
git merge --abort
```

取消合并不会删除合并前已经提交的二开代码。

## 推送二开分支

合并确认无误后推送：

```bash
git push -u fork local:local
```

以后只需：

```bash
git push fork local
```

如果远端 `local` 已有其他人的提交，先检查远端历史，不要直接强制推送：

```bash
git fetch fork local
git log --oneline --decorate --graph local fork/local -20
```

如果首次向空仓库推送时出现 `did not receive expected object` 或 `index-pack failed`，先确认本地是否为浅克隆：

```bash
git rev-parse --is-shallow-repository
```

如果结果为 `true`，从标准仓库补齐历史后再推送：

```bash
git fetch --unshallow origin
git fsck --full --no-dangling
git push --no-thin -u fork local:local
```

## 推荐的提交边界

二开代码、标准版合并、文档更新分别使用清晰的提交：

```text
feat/fix: 二开功能
Merge origin/main into local
docs: update main-local merge workflow
```

这样出现回归时，可以准确定位是二开改动还是标准版合并引入的问题。

## 本项目当前状态

本次已完成：

- 当前二开改动已提交到 `local`。
- `origin/main` 的最新标准版已合入 `local`。
- `fork` 已指向 `liaopj901/infinite-canas`。
- `.playwright-mcp/` 保持未跟踪，未纳入业务提交。
