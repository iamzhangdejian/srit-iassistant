// Package liccmd 提供一个最小的命令行入口，用于生成 Ed25519 密钥对、
// 签发授权码、校验授权码。
//
// 它存在的理由：发码管理端（cmd/licadmin）是一个带界面的独立服务，
// 但在首次部署、运维排障、以及「用户说码坏了让我看看」这些场景下，
// 开个网页太笨重。有了这个子命令，一行命令就能完成。
//
// 用法：
//
//	go run ./internal/license/liccmd keygen -out ~/.weknora-licadmin
//	go run ./internal/license/liccmd sign -key <私钥路径> -mac AA:BB:.. -days 365 -user 张三
//	go run ./internal/license/liccmd verify -pub <公钥路径> -code WNL1.xxx.yyy
package liccmd

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

const (
	privateKeyFile = "private.key"
	publicKeyFile  = "public.key"
	// 私钥权限：只有属主可读写。这是整个授权体系里唯一需要保密的东西。
	privateKeyMode = 0o600
	publicKeyMode  = 0o644
)

// Run 是命令行入口，返回进程退出码。
func Run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "keygen":
		return runKeygen(args[1:])
	case "sign":
		return runSign(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `授权码工具

子命令：
  keygen  生成 Ed25519 密钥对
  sign    用私钥签发一张授权码
  verify  用公钥校验一张授权码

示例：
  liccmd keygen -out ~/.weknora-licadmin
  liccmd sign -key ~/.weknora-licadmin/private.key \
      -mac 3c:22:fb:dd:ee:01,f0:18:98:dd:ee:02 \
      -days 365 -user 张三 -org 某某单位 \
      -feat kb,chat,ocr,office
  liccmd verify -pub ~/.weknora-licadmin/public.key -code WNL1.xxx.yyy
`)
}

func runKeygen(args []string) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", ".", "密钥输出目录")
	force := fs.Bool("force", false, "目录已有密钥时覆盖（危险：会让已发出的授权码全部失效）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	privPath := filepath.Join(*out, privateKeyFile)
	if !*force {
		if _, err := os.Stat(privPath); err == nil {
			fmt.Fprintf(os.Stderr, "错误：%s 已存在。覆盖会导致**已发出的所有授权码失效**。\n"+
				"确认要重新生成请加 -force。\n", privPath)
			return 1
		}
	}

	if err := os.MkdirAll(*out, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "创建目录失败：%v\n", err)
		return 1
	}
	if err := GenerateKeyPair(*out, *force); err != nil {
		fmt.Fprintf(os.Stderr, "生成密钥对失败：%v\n", err)
		return 1
	}

	fmt.Printf("密钥对已生成：\n  私钥 %s （权限 0600，务必离线备份，绝不进安装包）\n  公钥 %s （需要内置到客户端）\n",
		privPath, filepath.Join(*out, publicKeyFile))
	return 0
}

// GenerateKeyPair 在 dir 下生成 Ed25519 密钥对（private.key / public.key）。
//
// 单独导出是为了让双击版（cmd/licadmin -app-mode）复用同一套生成逻辑：
// 两处实现分叉会让「命令行能启、双击启不了」变成难以复现的偶发问题。
// force 为 false 时若私钥已存在则返回错误，避免误覆盖把已发的码全废掉。
func GenerateKeyPair(dir string, force bool) error {
	privPath := filepath.Join(dir, privateKeyFile)
	if !force {
		if _, err := os.Stat(privPath); err == nil {
			return fmt.Errorf("%s 已存在，覆盖会让已发出的所有授权码失效", privPath)
		}
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("生成密钥: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建目录: %w", err)
	}
	// 私钥用标准 base64 存文本，便于备份与迁移；内容仍是 64 字节种子+公钥。
	if err := os.WriteFile(privPath,
		[]byte(base64.StdEncoding.EncodeToString(priv)), privateKeyMode); err != nil {
		return fmt.Errorf("写私钥: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, publicKeyFile),
		[]byte(base64.StdEncoding.EncodeToString(pub)), publicKeyMode); err != nil {
		return fmt.Errorf("写公钥: %w", err)
	}
	return nil
}

// PrivateKeyFingerprint 返回私钥对应公钥的指纹（KeyID）。
//
// 发码端在「刚生成密钥」「密钥已存在」两种情况下都要把它摆出来：换钥
// 事故里最先需要核对的，就是「我现在用的这把，和客户端内置的那把，是不是
// 同一把」。指纹比整段 base64 好抄、好比对。
func PrivateKeyFingerprint(path string) (string, error) {
	priv, err := loadPrivateKey(path)
	if err != nil {
		return "", err
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("私钥不包含可用的公钥")
	}
	id := license.KeyID(pub)
	if id == "" {
		return "", fmt.Errorf("公钥长度异常，无法计算指纹")
	}
	return id, nil
}

// PublicKeyBase64Of 返回私钥对应公钥的 base64 文本。
//
// 用途很具体：把这段内容原样写进客户端源码的 config/license/public.key，
// 打包时就会注入到客户端里。少一次「手工从别处复制」就少一次抄错。
func PublicKeyBase64Of(path string) (string, error) {
	priv, err := loadPrivateKey(path)
	if err != nil {
		return "", err
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("私钥不包含可用的公钥")
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

func runSign(args []string) int {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyPath := fs.String("key", "", "私钥文件路径")
	macArg := fs.String("mac", "", "绑定的 MAC 地址，多个用逗号分隔")
	days := fs.Int("days", 365, "有效天数，0 表示永久")
	user := fs.String("user", "", "使用人")
	org := fs.String("org", "", "使用单位")
	featArg := fs.String("feat", "kb,chat,ocr,office", "功能位图，可选 kb,chat,ocr,office")
	sn := fs.String("sn", "", "授权序列号，留空自动生成")
	rst := fs.Bool("rst", false, "签发复位码：客户端导入时丢弃历史到期水位（仅在帮客户解死锁时使用）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *keyPath == "" || *macArg == "" {
		fmt.Fprint(os.Stderr, "错误：-key 与 -mac 都是必填项\n")
		return 2
	}

	priv, err := loadPrivateKey(*keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取私钥失败：%v\n", err)
		return 1
	}

	macs := license.NormalizeMACs(strings.Split(*macArg, ","))
	if len(macs) == 0 {
		fmt.Fprint(os.Stderr, "错误：没有解析出合法的 MAC 地址\n")
		return 2
	}

	feats, err := parseFeatures(*featArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		return 2
	}

	now := time.Now()
	serial := *sn
	if serial == "" {
		serial = fmt.Sprintf("SN-%s-%s", now.Format("20060102"), randomToken(4))
	}
	var exp int64
	if *days > 0 {
		exp = now.AddDate(0, 0, *days).Unix()
	}

	payload := license.Payload{
		V:    1,
		SN:   serial,
		FP:   license.Fingerprint(macs),
		MAC:  macs,
		IAT:  now.Unix(),
		EXP:  exp,
		Feat: feats,
		Org:  *org,
		User: *user,
		KID:  license.KeyID(priv.Public().(ed25519.PublicKey)),
		RST:  *rst,
	}
	code, err := license.Encode(payload, priv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "签发失败：%v\n", err)
		return 1
	}

	fmt.Printf("序列号  %s\n", serial)
	fmt.Printf("绑 MAC  %s\n", strings.Join(macs, ", "))
	fmt.Printf("指纹    %s\n", payload.FP)
	if exp == 0 {
		fmt.Printf("有效期  永久\n")
	} else {
		fmt.Printf("有效期  至 %s（%d 天）\n", time.Unix(exp, 0).Format("2006-01-02"), *days)
	}
	fmt.Printf("功能    %s\n", joinFeatures(feats))
	fmt.Printf("签发者  %s\n", license.KeyID(priv.Public().(ed25519.PublicKey)))
	if *rst {
		fmt.Printf("复位码  是（导入时将清空客户端历史到期水位）\n")
	}
	fmt.Printf("\n授权码：\n%s\n", code)
	return 0
}

func runVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	pubPath := fs.String("pub", "", "公钥文件路径")
	code := fs.String("code", "", "授权码文本")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pubPath == "" || *code == "" {
		fmt.Fprint(os.Stderr, "错误：-pub 与 -code 都是必填项\n")
		return 2
	}

	pub, err := loadPublicKey(*pubPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取公钥失败：%v\n", err)
		return 1
	}

	parsed, err := license.Decode(*code, pub)
	if err != nil {
		fmt.Fprintf(os.Stderr, "验签失败：%v\n", err)
		return 1
	}
	p := parsed.Payload

	// 用 JSON 输出，方便脚本消费；字段名与客户端日志保持一致，
	// 客服拿一条记录就能对照排查。
	out, _ := json.MarshalIndent(map[string]any{
		"serial":      p.SN,
		"fingerprint": p.FP,
		"macs":        p.MAC,
		"issued_at":   time.Unix(p.IAT, 0).Format(time.RFC3339),
		"expires_at":  expText(p.EXP),
		"features":    p.Feat,
		"org":         p.Org,
		"user":        p.User,
		"signature":   "valid",
	}, "", "  ")
	fmt.Printf("%s\n", out)
	return 0
}

// ---- 辅助 ----

func expText(exp int64) string {
	if exp == 0 {
		return "permanent"
	}
	return time.Unix(exp, 0).Format(time.RFC3339)
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(path); statErr == nil {
		// 私钥权限过宽时警告但不阻止：用户的备份介质可能没有 POSIX 权限。
		if info.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(os.Stderr, "警告：私钥 %s 权限为 %o，建议 chmod 600\n",
				path, info.Mode().Perm())
		}
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("不是合法的 base64：%w", err)
	}
	if len(data) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("私钥长度 %d，期望 %d", len(data), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(data), nil
}

func loadPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("不是合法的 base64：%w", err)
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("公钥长度 %d，期望 %d", len(data), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(data), nil
}

func parseFeatures(raw string) ([]license.Feature, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	out := make([]license.Feature, 0, 4)
	seen := make(map[license.Feature]struct{})
	for _, item := range strings.Split(raw, ",") {
		f := license.Feature(strings.TrimSpace(item))
		if f == "" {
			continue
		}
		if !license.IsKnownFeature(f) {
			return nil, fmt.Errorf("未知功能 %q，可选：%s", f, joinFeatures(license.AllFeatures))
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("至少需要指定一个功能")
	}
	return out, nil
}

func joinFeatures(fs []license.Feature) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = string(f)
	}
	return strings.Join(parts, ",")
}

// randomToken 生成 n 个字符的随机十六进制串，用于序列号的尾部。
func randomToken(n int) string {
	buf := make([]byte, (n+1)/2)
	if _, err := rand.Read(buf); err != nil {
		return "0000"
	}
	s := fmt.Sprintf("%x", buf)
	if len(s) > n {
		return s[:n]
	}
	return s
}
