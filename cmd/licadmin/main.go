// Command licadmin 是授权码发码管理端。
//
// 它是一个独立的小服务，与客户端严格分离：私钥只在这里，绝不打包进
// 桌面端安装包。默认只监听 127.0.0.1，需要局域网访问时必须显式加
// -allow-remote。
//
// 首次使用：
//
//	go run ./cmd/licadmin -data ./licadmin-data
//
// 首次部署必须显式执行一次 -init-key 生成密钥对（默认落在
// <data>/private.key）。**故意不做「文件不存在就自动生成」**：那样一次
// 数据目录变更就能悄悄换掉身份，已发出的授权码会集体失效，而客户端
// 打包又会照新公钥注入，最终表现为用户「授权码无效」——2026-11 的
// 生产事故就是这么发生的。
//
// 浏览器打开终端打印的地址后，先创建管理员账号。
//
// 非技术用户直接双击国研智研体授权管理.app 即可：那是一个以 -app-mode
// 启动的包装壳，会自动完成生成密钥、保存口令、打开浏览器三件事。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Tencent/WeKnora/internal/license/liccmd"
)

// buildVersion 由打包脚本通过 -ldflags "-X main.buildVersion=..." 注入。
// 留空表示本地直接 go run 编译，不是正式制品。
var buildVersion = "dev"

func main() {
	os.Exit(run())
}

// runningFromAppBundle 判断当前进程是不是从 .app 包里被双击起来的。
//
// 为什么必须自动判断：Finder 双击只会执行 Contents/MacOS/<可执行文件>，
// 不会传任何参数。之前 -app-mode 默认 false，等于双击走的是命令行分支——它
// 会去 mkdir 相对路径 "licadmin-data"，而 Finder 给的工作目录是 "/"，于是
// mkdir 失败、进程 exit 1。stdout 指向 /dev/null，用的人只看到「双击没反应、
// 浏览器连不上 8091」，日志里一个字都没有。这个函数把「是否在包里」当作
// 双击的充分信号，省掉一个靠人记得传的参数。
func runningFromAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return isAppBundlePath(exe)
}

// isAppBundlePath 判断可执行文件路径是否落在 .app 包内。
//
// 抽成纯函数是为了能测：双击行为靠它决定，写错了就是「双击没反应」，
// 而这个错误在自动化测试里很难靠真的双击发现。
func isAppBundlePath(exe string) bool {
	// 打包结构固定是 <名字>.app/Contents/MacOS/<二进制>。
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

func run() int {
	var (
		keyPath     = flag.String("key", "", "Ed25519 私钥文件路径（默认 <data>/private.key）")
		dataDir     = flag.String("data", "licadmin-data", "台账数据目录")
		addr        = flag.String("addr", "127.0.0.1", "监听地址，默认仅本机")
		port        = flag.Int("port", 8091, "监听端口")
		token       = flag.String("token", "", "自动化令牌（供脚本调用授权接口），留空则自动生成")
		allowRemote = flag.Bool("allow-remote", false, "允许非本机访问（谨慎使用）")
		logPath     = flag.String("log", "", "日志文件路径（仅供「系统 → 运行日志」查看，留空表示不写文件）")
		appMode     = flag.Bool("app-mode", runningFromAppBundle(), "双击版运行模式：口令存文件、自动开浏览器（密钥仍需显式初始化）")
		initKey     = flag.Bool("init-key", false, "首次部署时显式生成密钥对（仅当私钥不存在时生效）")
	)
	flag.Parse()

	// 双击启动时没有终端，出问题必须让人看见，不能用 WriteToStderr 静默退出。
	if *appMode {
		return runAppMode(*addr, *port, *allowRemote)
	}

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "创建数据目录失败：%v\n", err)
		return 1
	}

	// 私钥默认跟数据目录走。以前默认值是固定的 "licadmin-data/private.key"，
	// 只用 -data 换目录时私钥仍留在当前工作目录——数据目录搬走、私钥没搬，
	// 备份里就少一把必须有的东西。
	if *keyPath == "" {
		*keyPath = filepath.Join(*dataDir, "private.key")
	}
	if _, err := os.Stat(*keyPath); err != nil {
		// 私钥缺失。这里**不**自动生成：静默换身份是这套系统最危险的失败
		// 模式——已发出的码全部验签失败，而客户端打包会照新公钥注入，用户
		// 只会看到「授权码无效或已被篡改」。生成必须由人显式要求。
		if !*initKey {
			fmt.Fprintf(os.Stderr,
				"未找到私钥：%s\n\n"+
					"为避免意外更换签发身份（会让已发出的授权码全部失效），\n"+
					"本程序不会自动生成密钥。首次部署请显式执行：\n\n"+
					"  %s -data %s -init-key\n\n"+
					"生成后请立刻离线备份 private.key，并把 public.key 同步到\n"+
					"客户端源码的 config/license/public.key 后重新打包。\n"+
					"（如果你本来就想换目录沿用旧密钥，把旧 private.key 复制到该目录即可。）\n",
				*keyPath, filepath.Base(os.Args[0]), *dataDir)
			return 2
		}
		// 显式初始化：force=false 保证绝不会覆盖已有私钥。
		if kerr := liccmd.GenerateKeyPair(filepath.Dir(*keyPath), false); kerr != nil {
			fmt.Fprintf(os.Stderr, "生成私钥失败：%v\n", kerr)
			return 1
		}
		if err := printKeyFingerprint(*keyPath); err != nil {
			fmt.Fprintf(os.Stderr, "警告：无法打印公钥指纹：%v\n", err)
		}
		fmt.Fprintf(os.Stderr,
			"请立刻离线备份私钥——它丢失意味着以后无法再签发新码，\n"+
				"泄漏意味着任何人都能伪造授权码。\n\n")
	} else if *initKey {
		fmt.Fprintf(os.Stderr,
			"私钥已存在（%s），-init-key 被忽略——绝不覆盖现有密钥。\n\n", *keyPath)
		printKeyFingerprint(*keyPath)
	}

	consoleToken := strings.TrimSpace(*token)
	if consoleToken == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			fmt.Fprintf(os.Stderr, "生成访问口令失败：%v\n", err)
			return 1
		}
		consoleToken = hex.EncodeToString(buf)
	}

	// 装配交给共享函数：命令行模式与双击模式必须用同一套子库，
	// 否则打包出去的 app 会缺技能库/发布库（曾经就是这样漏的）。
	built, err := buildServer(serverBuildOptions{
		dataDir:     *dataDir,
		keyPath:     *keyPath,
		token:       consoleToken,
		allowRemote: *allowRemote,
		logPath:     *logPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer built.close()
	srv := built.srv

	listenAddr := net.JoinHostPort(*addr, fmt.Sprint(*port))
	httpServer := &http.Server{
		Addr:              listenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 端口被占用时自动往后找一个，避免运维时反复改参数。
	ln, actualAddr, err := listenWithFallback(*addr, *port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "监听失败：%v\n", err)
		return 1
	}

	displayHost := *addr
	if displayHost == "0.0.0.0" || displayHost == "::" {
		displayHost = "127.0.0.1"
	}
	_, actualPort, _ := net.SplitHostPort(actualAddr)
	fmt.Printf("授权码发码管理端已启动\n")
	fmt.Printf("  地址：http://%s\n", net.JoinHostPort(displayHost, actualPort))
	fmt.Printf("  自动化令牌：%s\n", consoleToken)
	fmt.Printf("  台账：%s\n", filepath.Join(*dataDir, "ledger.json"))
	fmt.Printf("  公钥：%s\n\n", built.publicKey)
	fmt.Printf("（浏览器请用管理员账号密码登录；首次启动会引导创建账号。\n")
	fmt.Printf("  自动化令牌仅供脚本调用授权接口，不能管理账号；重启会重新生成，固定值请用 -token 指定。）\n\n")

	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "服务异常退出：%v\n", err)
		}
	}()

	// 优雅退出：等信号再关，避免正在写台账时被强杀。
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	fmt.Println("\n已停止。")
	return 0
}

// listenWithFallback 在指定端口被占用时依次尝试后续端口（最多 20 个）。
//
// 这个便利性来自实际经验：发码端常与本机其他服务并存，端口冲突时让
// 人手工改参数的摩擦，远大于自动顺延带来的「端口不固定」的代价——
// 反正地址每次启动都会打印出来。
func listenWithFallback(host string, port int) (net.Listener, string, error) {
	var lastErr error
	for i := 0; i < 20; i++ {
		addr := net.JoinHostPort(host, fmt.Sprint(port+i))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, addr, nil
		}
		lastErr = err
	}
	return nil, "", lastErr
}
