package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/license"
	"github.com/Tencent/WeKnora/internal/licenser"
	"github.com/Tencent/WeKnora/internal/logger"
)

// 授权相关环境变量。
//
// 之所以用环境变量而不是配置项：公钥是「绑定到某个发行版本」的常量，
// 用户在界面上改它没有意义，只会在升级时造成困扰；而关闭授权的开关
// 只应存在于开发和内部构建中，不该出现在面向客户的设置页。
const (
	// licensePublicKeyEnv 是内置公钥。内容是 base64 编码的 32 字节
	// Ed25519 公钥（由 `go run ./cmd/lic keygen` 生成，取 public.key 的内容）。
	licensePublicKeyEnv = "WK_LICENSE_PUBLIC_KEY"
	// licenseDisableEnv 设为 1 时彻底关闭授权闸门，用于开发与回归测试。
	licenseDisableEnv = "WK_LICENSE_DISABLE"
)

// licensePublicKey 是编译期内置的公钥。
//
// 构建正式发行版时通过 -ldflags "-X main.licensePublicKey=<base64>" 注入，
// 这样发码端的私钥永远不必出现在源码树里，而打包脚本也能对每个渠道
// 使用不同的密钥对。留空表示本构建不带授权能力。
var licensePublicKey string

// setupDesktopLicense 初始化授权服务并返回它。
//
// 返回 nil 是合法的，含义是「本流程不启用授权闸门」：
//   - 设了 WK_LICENSE_DISABLE=1（开发/测试）；
//   - 没有内置公钥也没有环境变量（未经授权的内部构建）。
//
// 判定的顺序有讲究：先看显式关闭，再看有没有公钥。反过来的话，
// 内部构建会先因为「没有公钥」报错，而开发者只是没打算测授权。
func setupDesktopLicense() *licenser.Service {
	if strings.TrimSpace(os.Getenv(licenseDisableEnv)) == "1" {
		logger.Infof(context.Background(), "[license] disabled by %s, running unrestricted", licenseDisableEnv)
		return nil
	}

	// 公钥按「显式指定 > 编译期内置 > 打包时落盘文件」的顺序解析。
	//
	// 最后一级是给「不想抄一串 base64」的构建流程准备的：打包脚本把
	// 公钥写进 .app 的 Resources/public.key，客户端启动时直接读。
	// 这样运维永远不需要接触密钥概念。
	raw := resolveLicensePublicKey()
	if raw == "" {
		logger.Infof(context.Background(),
			"[license] no public key available (%s / -ldflags / Resources/public.key), running unrestricted",
			licensePublicKeyEnv)
		return nil
	}

	pub, err := license.ParsePublicKey(raw)
	if err != nil {
		// 公钥坏掉意味着所有授权码都验不过。这里必须 fail-closed：
		// 以前返回零值 Service，Enabled() 为 false，中间件直接放行，
		// 等于一次打包失误就把授权保护静默关掉。
		logger.Errorf(context.Background(), "[license] invalid public key, refusing to start without license: %v", err)
		return licenser.NewFailed(fmt.Sprintf("授权公钥无效（%v），请联系运维重新打包", err))
	}

	dir, err := desktopLicenseDir()
	if err != nil {
		logger.Errorf(context.Background(), "[license] cannot resolve license dir, refusing to start without license: %v", err)
		return licenser.NewFailed(fmt.Sprintf("授权目录不可用（%v），请检查用户目录权限", err))
	}

	store := license.NewStore(license.StoreOptions{
		Dir:       dir,
		PublicKey: pub,
		LocalMACs: license.CollectLocalMACs(),
	})
	// 审计写到 license 目录下的 license-audit.jsonl：与授权文件同处，
	// 用户迁移或备份时一并带走，排查时也不用满盘找。
	auditor := licenser.NewAuditor(dir)
	if auditor == nil {
		logger.Warnf(context.Background(), "[license] audit file unavailable under %s, continuing without it", dir)
	} else {
		logger.Infof(context.Background(), "[license] audit file: %s", auditor.Path())
	}

	svc := licenser.NewService(licenser.Options{
		Store:         store,
		Enabled:       true,
		Auditor:       auditor,
		OnStateChange: logLicenseTransition,
	})
	// 后台每小时重判一次授权状态。
	//
	// 桌面端用户习惯让应用连续开着数周，若只在启动时判定一次，
	// 就会出现「码早已过期、程序仍在正常使用」——既与「过期降级」
	// 的承诺不符，也让续期催办失去意义。
	svc.StartWatcher()

	ev := svc.Status()
	fp, macs := svc.MachineFingerprint()
	logger.Infof(context.Background(),
		"[license] state=%s fp=%s macs=%v license_dir=%s",
		ev.State, fp, macs, dir)
	if ev.Reason != "" {
		logger.Infof(context.Background(), "[license] reason=%s", ev.Reason)
	}
	return svc
}

// desktopLicenseDir 返回授权文件的存放目录。
//
// 复用与 desktop-prefs.json 相同的目录：用户备份或迁移配置时，
// 授权文件跟着走，不会出现「配置迁过去了但要重新激活」的情况。
func desktopLicenseDir() (string, error) {
	dir, err := desktopPrefsDir()
	if err != nil {
		return "", err
	}
	sub := filepath.Join(dir, "license")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return "", err
	}
	return sub, nil
}

// logLicenseTransition 在授权状态跃迁时记录日志。
//
// 只记跃迁不记每次重判：valid→valid 每小时都会发生，全记下来会把
// 真正有用的信息（某台在用机器掉了授权）埋掉。
func logLicenseTransition(prev, next license.Evaluation) {
	ctx := context.Background()
	// 掉授权是必须让人看见的事件，用 Warn；好转用 Info。
	switch {
	case next.State == license.StateValid && prev.State != license.StateValid:
		logger.Infof(ctx, "[license] recovered: %s -> %s (expires=%d)",
			prev.State, next.State, next.ExpiresAt)
	case next.State == license.StateReadOnly || next.State == license.StateInvalid:
		logger.Warnf(ctx, "[license] degraded: %s -> %s reason=%s remaining_days=%d",
			prev.State, next.State, next.Reason, next.RemainingDays)
	default:
		logger.Infof(ctx, "[license] transition: %s -> %s remaining_days=%d",
			prev.State, next.State, next.RemainingDays)
	}
}

// resolveLicensePublicKey 依次尝试三个来源，返回第一个非空值。
//
// 顺序刻意固定：显式环境变量用于排障与私有构建；-ldflags 注入是正式
// 发行版的常规做法；Resources/public.key 是「打包时顺手带上」的兜底，
// 让构建脚本不必再拼 -ldflags 字符串。
func resolveLicensePublicKey() string {
	if v := strings.TrimSpace(os.Getenv(licensePublicKeyEnv)); v != "" {
		return v
	}
	if v := strings.TrimSpace(licensePublicKey); v != "" {
		return v
	}
	return readBundledPublicKey()
}

// readBundledPublicKey 从 .app 的 Resources 目录读 public.key。
//
// 找不到就返回空串（不报错）：开发环境下直接 go run 时没有这个文件，
// 属于正常情况。路径解析失败同样静默——授权是否启用由调用方按「空串」
// 决定，这里不该替它做判断。
func readBundledPublicKey() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return readBundledPublicKeyFrom(exe)
}

// bundledPublicKeyPaths 列出 public.key 的候选位置，覆盖三种打包布局：
//  1. macOS .app ：<bin>/../Resources/public.key
//  2. Windows 安装目录 / Linux AppImage 的 usr/bin：与可执行文件同目录
//  3. 资源根（启动时已 chdir 到 config 所在目录）：./public.key
func bundledPublicKeyPaths(exe string) []string {
	dir := filepath.Dir(exe)
	return []string{
		filepath.Join(dir, "public.key"),
		filepath.Join(dir, "..", "Resources", "public.key"),
		"public.key",
	}
}

func readBundledPublicKeyFrom(exe string) string {
	for _, candidate := range bundledPublicKeyPaths(exe) {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		if key := strings.TrimSpace(string(data)); key != "" {
			return key
		}
	}
	return ""
}
