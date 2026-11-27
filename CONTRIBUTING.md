# 开发与交付规范（国研智研体 OEM）

本仓库 `weknora_src` 是**唯一主仓库**，同时承担 PC 客户端（Wails）与发码端（授权管理）的构建。
本文件规定分支、提交、打包、发布、密钥与备份的做法。改流程请改这个文件，别靠口口相传。

---

## 1. 分支、提交、版本

### 1.1 `main` 的定义

`main` 必须**随时可打包、可交付**。合进 `main` 之前至少要能通过：

- `go build -tags "sqlite_fts5" ./cmd/server`
- `cd frontend && ./node_modules/.bin/vue-tsc --noEmit`
- 对应改动的单测（授权相关：`go test ./internal/license/... ./internal/licenser/...`）

`main` 受分支保护：禁止强推、禁止删除。历史一旦被冲掉，本地 reflog 只有 90 天可救。

### 1.2 分支命名

| 场景 | 命名 | 生命周期 |
|---|---|---|
| 功能 | `feat/<主题>` | 合并后删除 |
| 修缺陷 | `fix/<问题>` | 合并后删除 |
| 杂务（依赖/脚本） | `chore/<主题>` | 合并后删除 |
| 长期并行线（Windows / Linux 打包） | `feat/windows-build`、`feat/linux-portable` | 常驻，定期 rebase `main` |

单人开发允许直接提交 `main`，但**并行会话必须用独立 worktree**（见第 7 节）。

### 1.3 提交信息

```
type(scope): 一句话说清做了什么

正文写"为什么这么改"和"怎么验证的"。实测数据直接贴：版本号、字节数、
退出码、接口响应片段。别写"优化了一下"这种无法复核的话。
```

`type` 取值：`feat` / `fix` / `docs` / `chore` / `ci` / `refactor` / `test`。

一个改动一条提交。不要把"产物入库 + 功能改动 + 格式化"混在一起——回滚时会连坐。

### 1.4 版本号与标签

- 代码版本 `0.9.x`；交付打标签 `v0.9.x-YYYYMMDD`（带日期便于和客户报障时间对账）。
- 交付文档（仓库外 `WeKnora-OEM-交付文档.md`）头部有独立版本号（v3.x），两者不是一回事，但发布说明里要写"产品版本 ↔ 代码 tag ↔ 文档版本"的对应关系。

---

## 2. 构建与打包

### 2.1 前端必须先手工重建

`wails build` 的日志是 `No Build command. Skipping.`，它**不会**帮你构建前端，只把现有 `frontend/dist` 拷进 app。改了任何 `.vue` / `.ts` 都必须：

```bash
cd frontend && export PATH=../../tools/node24/bin:$PATH && ./node_modules/.bin/vite build
```

### 2.2 三个构建入口

| 目标 | 命令 | 产物 |
|---|---|---|
| PC 客户端（macOS） | `bash scripts/oem-build-lite.sh` | `cmd/desktop/build/bin/国研智研体.app` + zip |
| PC 客户端（Windows） | GitHub Actions `desktop-windows`（手动触发） | `.exe` + NSIS 安装包 |
| 发码端（原生窗口） | `NATIVE=1 bash scripts/package-licadmin.sh` | `cmd/licadmin/build/国研智研体授权管理.app` + zip |

桌面目标**不能**用裸 `go build -tags "desktop production ..."` 验证——链接期会报
`ld: unknown file type in /var/.../go-link-*/000000.o`。原因见 `oem-build-lite.sh` 里额外设置的
`CGO_LDFLAGS="-Wl,-no_warn_duplicate_libraries"`。要验证编译就用该脚本或 `wails build`。

DMG 生成偶发失败（`OSStatus error 1`），脚本会自动回退为 zip，属已知行为，不影响交付。

### 2.3 产物永不入库

`dist_install/` 是本地产物区；`cmd/desktop/build/bin/`、`cmd/licadmin/build/`、`frontend/node_modules/`、
`tools/`、`*.bundle` 均被 `.gitignore` 排除。`cmd/desktop/build/` 下的 `appicon.png`、`darwin/Info.plist`、
`windows/*` 是 Wails 脚手架文件，**必须入库**，别误删。

---

## 3. 交付五步

完整勾选清单见 [`.github/RELEASE-CHECKLIST.md`](.github/RELEASE-CHECKLIST.md)。

1. 工作区干净 + 与远端同步（`git status` / `git pull --ff-only`）
2. 重建前端 → 打包客户端与发码端
3. 硬门槛校验：三方 KID 一致、签名有效、冒烟启动
4. 打标签推送 + GitHub Release 上传两个 zip
5. 交付文档补实测行（`§9.3` 授权链路、`§9.6` 构建实测）

---

## 4. 发布

- 产物作为 **Release 资产**上传，不进仓库历史。
- Release 说明至少包含：对应代码 tag、交付文档版本、包大小、`codesign --verify` 结果、已知限制。
- 发码端的版本与客户端版本**同 tag** 发布，避免"客户端更新了、发码端还是旧的"导致公钥对不上。

---

## 5. 密钥与敏感信息

| 材料 | 位置 | 是否入库 |
|---|---|---|
| 客户端信任的签发公钥 | `config/license/public.key` | **入库**（公开材料，打包时嵌入） |
| 签发私钥 | `~/Library/Application Support/国研智研体授权管理/licadmin-data/private.key` | 否 |
| 发码台账 | 同上目录 `licadmin.db` | 否 |
| 运行时数据/审计日志 | `~/Library/Application Support/国研智研体/`、便携模式 `dist_install/数据/` | 否 |

纪律：

1. 私钥只存在发码机器上，**离线单独备份**（U 盘 / 密码管理器）。私钥丢失 = 已发出的授权码无法再续签同族。
2. 提交前自查：`git diff --cached --name-only | grep -iE 'private\.key|\.env$|token'`（或接 `cli/scripts/check-secret-tokens.sh` 为 pre-commit）。
3. 万一误提交：**先作废/轮换凭据**，再 `git filter-repo` 洗历史并强推，最后通知所有 clone 重拉。顺序不能反。
4. 需要给客户源码时用 `git archive` 导快照或临时 Deploy Key，**不要**给人加协作者。

---

## 6. 备份与恢复

私有仓是单点，不是备份。

```bash
# 每月一次，或大改之后
mkdir -p ../backup
git bundle create ../backup/weknora_src-$(date +%Y%m%d).bundle --all

# 立刻验证备份可用（别只生成不验）
rm -rf /tmp/verify && git clone ../backup/weknora_src-$(date +%Y%m%d).bundle /tmp/verify \
  && git -C /tmp/verify log --oneline -1
```

恢复：`git clone <bundle> repo && cd repo && git remote set-url origin <GitHub URL> && git push -u origin main`。

---

## 7. 并行开发（多会话 / 多人）

同一个目录里跑两条构建或两条提交线会互相踩（并发写 `.git`、抢工作区文件）。用 worktree：

```bash
git worktree add ../wk-linux feat/linux-portable
git worktree add ../wk-win   feat/windows-build
# …各自开发、各自提交，最后统一 push…
git worktree remove ../wk-linux
```

约定：同一时间只有一条线做「改历史 / 强推 / 切分支」这类全局操作。

---

## 8. CI 策略（私有仓计费）

私有仓免费额度 2000 分钟/月，Windows runner 按 2 倍计费。仓库里多数 workflow 是从上游继承的，
与本项目交付无关，默认应**停用**（Settings → Actions → 选中 → Disable workflow，UI 操作不产生提交）。

保留建议：

| Workflow | 状态 | 理由 |
|---|---|---|
| `desktop-windows.yml` | 保留，仅 `workflow_dispatch` | Windows 包只能靠它出 |
| `go-lint.yml` | 可选保留 | Ubuntu，成本低，提交前把关 |
| 其余（docker-image / release-lite / helm / docreader / mcp-server / cli / anydoc / dsh-plugin / app / frontend …） | 停用 | 与本项目交付无关，纯烧额度 |

新增 workflow 必须写 `paths:` 过滤，禁止无条件 `on: push`。

---

## 9. 故障处置速查

| 现象 | 处置 |
|---|---|
| 误 force push 冲掉提交 | `git reflog` 找旧 tip → 用 `--force-with-lease` 推回；先靠分支保护预防 |
| 仓库对象损坏 | `git fsck --full` 定位 → 从最近 bundle 克隆重建后推回 |
| 授权码报"无效或已被篡改" | `lic verify -pub config/license/public.key -code <码>` 验签；再对比 `GET /api/v1/license` 的 `key_id` / `code_key_id` |
| 授权码报"到期早于已用过的授权" | 水位拦截：让发码方签一张不早于历史水位的码，或用 `-rst` 复位码 |
| 打包时提示公钥与发码端不一致 | 三方体检 `bash scripts/check-license-keys.sh`，确认以 `config/license/public.key` 为准 |
| 想借上游某个修 | `git remote add upstream-once <地址>` 临时 fetch / cherry-pick，用完**删掉远端** |