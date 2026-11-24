package main

// 双击版运行模式（-app-mode）。
//
// 为什么需要这个模式：命令行参数对运维人员没问题，但要发给「不懂终端」
// 的同事就不行了——他们不知道 cd、不知道口令、关掉终端就没了。这里把
// 三个动作自动化：生成密钥、把口令落到文件、启动后自动开浏览器。
//
// 关键约束：双击启动时进程没有终端附着（stdout 指向 /dev/null），任何
// fmt.Println 都看不到。所以所有反馈走两条路——写日志文件、弹系统对话框。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Tencent/WeKnora/internal/license/liccmd"
)

// 双击版固定使用的文件名与端口。
const (
	appModeDataDir = "licadmin-data"
	appModeKeyFile = "private.key"
	// 阶段 2 起浏览器走账号登录，这个文件只剩「脚本用自动化令牌」。
	appModeTokenFile = "自动化令牌.txt"
	// appModeLegacyTokenFile 是旧版本写的文件名，升级后继续读，避免
	// 已经存下的令牌失效（脚本里可能写死了它）。
	appModeLegacyTokenFile = "访问口令.txt"
	appModeLogFile         = "licadmin.log"
	appModePort            = 8091
)

// nativeShellOptions 是「把界面装进原生窗口」所需要的全部输入。
//
// 单独抽一层的理由：窗口实现（native.go）要按构建标签隔离——普通命令行
// 二进制不该为了管理端多背一个 WebKit/cgo 依赖，也不该在 CI 的
// CGO_ENABLED=0 环境里编不过。
type nativeShellOptions struct {
	URL     string
	DataDir string
	Built   *builtServer
	Logf    func(format string, args ...any)
	Version string
}

// nativeShellStart 由 native.go（构建标签 licadmin_native）注入。
//
// 为 nil 表示这个二进制没有编入窗口能力：此时一律退回「打开默认浏览器」，
// 行为与以前完全一致。
var nativeShellStart func(nativeShellOptions)

// runAppMode 是双击版的入口，返回进程退出码。
func runAppMode(addr string, port int, allowRemote bool) int {
	bootLog("进入双击模式，可执行文件 %s", executablePath())
	dataDir, err := appModeDataDirPath()
	if err != nil {
		appModeFail("无法确定数据目录", err)
		return 1
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		appModeFail("无法创建数据目录\n\n"+dataDir, err)
		return 1
	}

	// 双击第二次会起第二个进程，而它要开同一个 licadmin.db。
	// 以前靠端口顺延默默起在 8092，等于同一个人拿着两份窗口改同一份台账，
	// 恢复包、备份、审计都会打架。这里先探一下「本机是否已经有一个我们在跑」，
	// 有就提示并退出，把用户引导到已经打开的那个窗口。
	if probeExistingInstance(addr, port) {
		appModeFail("授权管理端已经在运行",
			fmt.Errorf("本机 http://%s 上已经有一个授权管理端窗口。\n\n请使用那个窗口（或先把它的窗口关掉再重新打开）。", net.JoinHostPort(displayHostFor(addr), fmt.Sprint(port))))
		return 0
	}

	logPath := filepath.Join(dataDir, appModeLogFile)
	logf := appModeLogger(logPath)
	logf("启动，数据目录 %s", dataDir)

	keyPath := filepath.Join(dataDir, appModeKeyFile)
	if _, err := os.Stat(keyPath); err != nil {
		// 私钥缺失：必须由人点一下确认才生成。
		//
		// 以前这里直接生成，代价是「换一次数据目录 = 换一次签发身份」，
		// 已发出的码集体失效。双击版没有终端，没法让用户看命令行提示，
		// 所以用系统对话框把后果讲清楚，默认按钮停在「取消」上——这是
		// 唯一一次需要用户明确同意才改变身份的地方。
		logf("未找到私钥，等待用户确认是否生成")
		if !appModeConfirmNewKey(keyPath) {
			logf("用户取消了密钥生成，退出")
			appModeFail("未找到私钥，已取消生成",
				fmt.Errorf("没有私钥就无法签发授权码。\n\n如果你有旧的 private.key，"+
					"把它复制到：\n%s\n\n如果你确实要新建一套身份（注意：新密钥签的码"+
					"在旧客户端上全部无效），重新打开并选择「生成新密钥」。", filepath.Dir(keyPath)))
			return 2
		}
		if err := appModeGenerateKeys(dataDir); err != nil {
			appModeFail("生成密钥对失败", err)
			return 1
		}
		logf("密钥对已生成")
		appModeFail("已生成新的签发密钥", fmt.Errorf(
			"公钥指纹：%s\n\n请立刻离线备份 private.key。\n"+
				"并确认客户端 config/license/public.key 与此公钥一致后重新打包，\n"+
				"否则这台机器签出的授权码在客户端会显示「无效」。", keyFingerprintOf(keyPath)))
	}

	token, err := appModeLoadToken(dataDir)
	if err != nil {
		appModeFail("读写访问口令失败", err)
		return 1
	}

	// 装配复用同一份实现：双击版曾经自己写一套，结果漏了技能库与发布库，
	// 打包出去的 app 里那两个视图全是 404。
	built, err := buildServer(serverBuildOptions{
		dataDir:     dataDir,
		keyPath:     keyPath,
		token:       token,
		allowRemote: allowRemote,
		logPath:     logPath,
	})
	if err != nil {
		appModeFail("启动管理端失败", err)
		return 1
	}
	defer built.close()
	srv := built.srv

	ln, actualAddr, err := listenWithFallback(addr, port)
	if err != nil {
		// 端口被占用是最常见的启动失败，单独给可操作的提示。
		appModeFail(fmt.Sprintf("端口 %d 及后续 20 个端口都被占用", port), err)
		return 1
	}
	httpServer := &http.Server{
		Addr:              actualAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	displayHost := addr
	if displayHost == "0.0.0.0" || displayHost == "::" {
		displayHost = "127.0.0.1"
	}
	_, actualPort, _ := net.SplitHostPort(actualAddr)
	url := "http://" + net.JoinHostPort(displayHost, actualPort)

	logf("监听 %s，自动化令牌文件 %s", url, filepath.Join(dataDir, appModeTokenFile))

	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			logf("服务异常退出：%v", err)
			appModeFail("服务异常退出", err)
		}
	}()

	if nativeShellStart != nil {
		// 原生窗口版（-tags licadmin_native）：界面装在独立窗口里，不碰浏览器。
		// 关掉窗口就等于退出，所以这里不等信号——信号路径留给浏览器模式。
		logf("原生窗口模式，界面地址 %s", url)
		nativeShellStart(nativeShellOptions{
			URL:     url,
			DataDir: dataDir,
			Built:   built,
			Logf:    logf,
			Version: buildVersion,
		})
		logf("原生窗口已关闭，正在停止服务")
	} else {
		// 打开不带令牌的地址：阶段 2 起浏览器身份是账号会话，把自动化令牌
		// 放进 URL 会绕过登录（也等于把脚本凭据写进浏览历史与截图里）。
		openURL := url + "/"

		// 稍等一下再开浏览器：Application 刚起来时 LaunchServices 还没就绪，
		// 立刻 open 偶尔会失败并弹一个与事实不符的「无法打开」。
		time.AfterFunc(600*time.Millisecond, func() {
			if err := exec.Command("open", openURL).Start(); err != nil {
				logf("打开浏览器失败：%v", err)
			}
		})

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	logf("已停止")
	return 0
}

// appModeDataDirPath 返回双击版的数据目录。
//
// 放在 $HOME/Library/Application Support 下，而不是 .app 包内：
// .app 里的内容在覆盖安装或系统校验签名时可能被重置，把私钥写在里面
// 等于埋一颗「升级后授权全废」的雷。
func appModeDataDirPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "国研智研体授权管理", appModeDataDir), nil
}

// isValidLegacyToken 判断旧版口令文件里是否只有一把令牌。
//
// 旧文件是带说明文字的多行文本（「访问口令：xxx」），直接当令牌用会得到
// 一个带中文的垃圾字符串；只有整份文件就是一行十六进制时才算有效。
func isValidLegacyToken(s string) bool {
	if s == "" || strings.ContainsAny(s, " \n\r：:") {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// appModeGenerateKeys 生成 Ed25519 密钥对，复用 cmd/lic 的同一套实现。
func appModeGenerateKeys(dir string) error {
	return liccmd.GenerateKeyPair(dir, true)
}

// appModeConfirmNewKey 用系统对话框请求用户明确同意生成新密钥。
//
// 默认按钮是「取消」：这个动作会让所有已发出的授权码失效，误触的代价
// 远高于多点一次的成本。
func appModeConfirmNewKey(keyPath string) bool {
	msg := "未找到私钥：\n" + keyPath + "\n\n" +
		"生成一套**新**的签发密钥会让已发出的所有授权码失效。\n" +
		"如果你只是换了数据目录，请先把旧的 private.key 复制过去。\n\n" +
		"确实要新建一套身份吗？"
	msg = strings.ReplaceAll(msg, `"`, `'`)
	script := fmt.Sprintf(
		`display dialog "%s" buttons {"取消", "生成新密钥"} default button 1 with icon caution with title "授权管理端"`,
		msg)
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		// 没有图形环境（远程 shell、CI）时不能默认「同意」：宁可失败。
		fmt.Fprintf(os.Stderr, "无法弹出确认框，已拒绝生成密钥：%v %s\n", err, string(out))
		return false
	}
	return strings.Contains(string(out), "生成新密钥")
}

// keyFingerprintOf 读出私钥对应的公钥指纹，读不出来返回「未知」。
func keyFingerprintOf(keyPath string) string {
	fp, err := liccmd.PrivateKeyFingerprint(keyPath)
	if err != nil {
		return "未知（" + err.Error() + "）"
	}
	return fp
}

// appModeLoadToken 读出已有口令；没有就生成一个并存盘。
//
// 口令要跨重启保持稳定：双击版的 .app 没有终端可以查看新口令，
// 每次重启都换口令会让用户每次都要去翻文件。
func appModeLoadToken(dir string) (string, error) {
	path := filepath.Join(dir, appModeTokenFile)
	if data, err := os.ReadFile(filepath.Join(dir, appModeLegacyTokenFile)); err == nil {
		if token := strings.TrimSpace(string(data)); isValidLegacyToken(token) {
			return token, nil
		}
	}
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// 8 字节 = 16 位十六进制：够防误触，又不至于难抄。
	// 这个口令挡的是「同一台电脑上的其他程序」，不是网络攻击——
	// 服务本身只监听 127.0.0.1。
	token := hex.EncodeToString(buf)
	body := "自动化令牌：" + token + "\n\n" +
		"浏览器里请用管理员账号密码登录（首次启动会引导创建账号）。\n" +
		"如果浏览器没有自动打开，手动访问：\n" +
		"  http://127.0.0.1:8091/\n\n" +
		"这个令牌只给脚本/CI 调用授权接口用，不能管理账号：\n" +
		"  curl -H \"X-Licadmin-Token: " + token + "\" http://127.0.0.1:8091/api/stats\n\n" +
		"令牌长期有效，重启应用不会改变。\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

// appModeLogger 返回一个同时写日志文件与 stdout 的打印函数。
//
// 双击启动时 stdout 无人接收，写文件是唯一的留痕手段；
// 从终端手动以 -app-mode 启动时又能直接在屏幕上看到。
func appModeLogger(path string) func(format string, args ...any) {
	return func(format string, args ...any) {
		line := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
		fmt.Print(line)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString(line)
	}
}

// appModeFail 弹出系统对话框告知失败原因。
//
// 双击启动最大的坑是「双击没反应」——进程退出码对用户不可见。
// 用 osascript 弹框把原因摆到屏幕上，是唯一能让非技术用户看懂的方式。
// 弹框本身失败（例如缺少 osascript）时降级为写 stderr，不影响退出。
// bootLogPath 是「连数据目录都没建起来」时唯一的留痕位置。
//
// 双击启动失败最常见的就是数据目录建不起来，而那时正式日志文件还没有
// 落点；/tmp 在所有环境下都可写，先用它兜住最早的一段。
const bootLogPath = "/tmp/licadmin-boot.log"

// bootLog 把启动早期的事件追加到 /tmp/licadmin-boot.log。
func bootLog(format string, args ...any) {
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	f, err := os.OpenFile(bootLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// executablePath 返回当前可执行文件的真实路径（取不到就返回空）。
func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

func appModeFail(title string, err error) {
	msg := title
	if err != nil {
		msg += "\n\n" + err.Error()
	}
	bootLog("启动失败：%s %v", title, err)
	msg += "\n\n启动日志：" + bootLogPath
	msg = strings.ReplaceAll(msg, `"`, `'`)
	script := fmt.Sprintf(`display dialog "%s" buttons {"好"} default button 1 with icon caution with title "授权管理端"`, msg)
	if out, e := exec.Command("osascript", "-e", script).CombinedOutput(); e != nil {
		fmt.Fprintf(os.Stderr, "%s\n%v\nosascript: %v %s\n", title, err, e, string(out))
	}
}

// instanceMarker 是「这个端口上跑的是我们自己」的判据。
//
// 直接探 TCP 不够：8091 被别的软件占用时也会连接成功，那会把用户拦在
// 「已经在运行」外面、实际却打不开。所以取首页看标题是否是我们这页。
const instanceMarker = "国研智研体 · 授权管理"

// probeExistingInstance 判断该端口上是否已经有一个授权管理端在跑。
func probeExistingInstance(addr string, port int) bool {
	host := displayHostFor(addr)
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get("http://" + net.JoinHostPort(host, fmt.Sprint(port)) + "/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return false
	}
	return strings.Contains(string(body), instanceMarker)
}

// displayHostFor 把监听地址转成可访问地址（0.0.0.0/:: 对外就是本机回环）。
func displayHostFor(addr string) string {
	if addr == "0.0.0.0" || addr == "::" || addr == "" {
		return "127.0.0.1"
	}
	return addr
}
