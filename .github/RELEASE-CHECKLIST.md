# 发布前勾选清单

每次交付（发新包给客户）逐条勾选。任何一条打不了勾就**不要发**。
规范说明见仓库根目录 [`CONTRIBUTING.md`](../CONTRIBUTING.md)。

版本信息（填好再往下走）

- 产品版本：`0.9.__`
- 代码 tag：`v0.9.__-20______`
- 交付文档版本：`v3.__`
- 发码端版本：与客户端同 tag ☐

---

## 一、代码就位

- [ ] `git status` 干净（没有未提交改动、没有未跟踪的源码文件）
- [ ] `git pull --ff-only` 已同步到 `origin/main` 最新
- [ ] `git log --oneline -5` 确认本次交付包含的提交都在，没有混入无关提交
- [ ] 后端能编译：`go build -tags "sqlite_fts5" ./cmd/server`
- [ ] 前端依赖已就位（`frontend/node_modules` 存在；缺失则 `cd frontend && npm ci`）
- [ ] 前端类型干净：`cd frontend && ./node_modules/.bin/vue-tsc --noEmit`
- [ ] 相关单测通过（授权相关必跑）
      `go test ./internal/license/... ./internal/licenser/... ./internal/application/service/`

## 二、打包

- [ ] 前端已重建（**wails 不会自动构建前端**）
      `cd frontend && export PATH=../../tools/node24/bin:$PATH && ./node_modules/.bin/vite build`
- [ ] 客户端打包成功：`bash scripts/oem-build-lite.sh`
- [ ] 打包日志里出现 `✓ 已嵌入授权公钥（KID …，来源 config/license/public.key）`
- [ ] 发码端打包成功：`NATIVE=1 bash scripts/package-licadmin.sh`
- [ ] 两个产物时间戳是本次构建（不是上次遗留）

## 三、硬门槛校验

- [ ] 三方公钥一致：`bash scripts/check-license-keys.sh "cmd/desktop/build/bin/国研智研体.app"`
      结果应为版本库 / 发码端 / 安装包三行 KID 完全相同
- [ ] 签名有效：`codesign --verify --deep --strict "cmd/desktop/build/bin/国研智研体.app"`
- [ ] 发码端签名有效：`codesign --verify --deep --strict "cmd/licadmin/build/国研智研体授权管理.app"`
- [ ] 客户端冒烟：`open "cmd/desktop/build/bin/国研智研体.app"` → 能进界面、`curl -s localhost:8740/api/v1/license` 返回 JSON
- [ ] 系统快捷键：窗口菜单栏含 `Window` 段，`⌘M` 最小化、`⌘W`/`⌘Q` 退出
      （用 `osascript` 的 `key code` 验证，**不要**用 `keystroke "w" using command down`——它走输入法层，匹配不到菜单项）
- [ ] 发码端冒烟：能开、能查台账、`签发说明` 处显示正确 KID
- [ ] 授权链路抽测：用发码端签一张短码 → 客户端导入成功 → 状态 `valid`

## 四、归档与发布

- [ ] 产物复制进 `dist_install/`（旧构建移到 `dist_install/_旧构建备份-YYYYMMDD/`）
- [ ] 打标签并推送：`git tag v0.9.__-20______ && git push origin main --tags`
- [ ] GitHub Release 已创建，上传两个 zip：
      - `国研智研体-0.9.__-macOS-arm64.zip`
      - `国研智研体授权管理-0.9.__-macOS.zip`
- [ ] Release 说明写明：对应 tag、交付文档版本、包大小、签名校验结果、已知限制

## 五、文档与备份

- [ ] 交付文档 `§9.3`（授权链路）补本次实测行
- [ ] 交付文档 `§9.6`（构建实测）补本次实测行
- [ ] 交付文档头部版本号递增
- [ ] 更新备份：`git bundle create ../backup/weknora_src-$(date +%Y%m%d).bundle --all` 并验一次可克隆
- [ ] 私钥备份仍可读（U 盘 / 密码管理器抽查）

---

## 附：本次已知限制（每次发布时更新）

- DMG 生成偶发 `OSStatus error 1`，自动回退 zip；客户拿 zip 解压即用
- 便携模式数据目录 `dist_install/数据/` 与常规目录是两套独立授权状态，换模式前确认水位
- 首发新机器首次 `open` 可能需 20s–2min 才起后端（大 bundle 首次校验），不要误判为启动失败