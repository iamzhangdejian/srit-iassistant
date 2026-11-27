//go:build !bindings

package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/handler"
	sessionhandler "github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/logger"
	goruntime "runtime"

	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types/interfaces"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/dig"
)

//go:embed build/appicon.png
var desktopIcon []byte

// dragHandlerJS is injected into the webview on DomReady.
// It bypasses Wails' built-in CSS-variable-based drag detection (which uses
// getComputedStyle and has timing/inheritance issues with dynamic SPA content)
// and instead uses robust DOM-traversal via el.closest() plus a Y-position
// fallback for the top 40px of a layout container, including its children.
// The "drag"
// message is sent directly through the WKWebView script-message bridge,
// which the native Objective-C handler in WailsContext.m converts to
// [NSWindow performWindowDragWithEvent:].
const dragHandlerJS = `(function(){
if(window.__wkDragBound)return;
window.__wkDragBound=true;
document.documentElement.classList.add('wails-desktop');

// Disable rubber-band overscroll that reveals the dark window background
document.documentElement.style.overscrollBehavior='none';
document.body.style.overscrollBehavior='none';

if(window.wails&&window.wails.flags){
  window.wails.flags.cssDragProperty='__disabled__';
  window.wails.flags.cssDragValue='__never__';
}

// Prevent native text/image drag-out to fix the "selected and dragged away" issue
window.addEventListener('dragstart', function(e){
  e.preventDefault();
}, true);

// OEM：顶部拖动回退只在「内嵌标题栏」模式下才启用。
// TitleBarHiddenInset 时 FullSizeContent=true，webview 铺满整个窗口，
// 最上面 40px 没有原生标题栏可拖，所以需要这个 Y 位置回退；
// 换成 TitleBarDefault 后系统标题栏独占顶部，webview 从标题栏下方开始，
// 此时若仍启用回退，会把内容区顶部的按钮/输入框误判成拖动区。
// 运行时由 Go 侧注入 __WK_EMBEDDED_TITLEBAR__（见 desktopTitlebarModeJS）。
var EMBEDDED_TITLEBAR = (window.__WK_EMBEDDED_TITLEBAR__ === true);

var TITLEBAR_H=40;

// We specifically look for Wails' inline style attributes injected by Vue,
// and custom drag classes, avoiding generic headers like .section-header.
// .chat-topbar is the window top on the conversation page.
var dragSel='.logo_row,.sidebar_winctl,.menu_top,.chat-topbar,.drag-region,[data-wails-drag],' +
  '[style*="--wails-draggable: drag"],[style*="--wails-draggable:drag"]';

var noDragSel='button,a,input,select,textarea,[role="button"],' +
  '.t-button,.t-input,.t-select,.t-textarea,' +
  '.header-actions,.header-action-btn,.sidebar-toggle,.logo_actions,.logo_box,' +
  '.close-btn,.menu_item,.submenu,.submenu_item,.menu_bottom,' +
  '.t-popup,.t-dropdown,.t-tooltip,.t-dialog,[data-no-drag],' +
  '[style*="--wails-draggable: no-drag"],[style*="--wails-draggable:no-drag"]';

var layoutClasses=['main','chat','dialogue-wrap','kb-list-container','kb-list-content',
  'agent-list-container','agent-list-content','org-list-container','org-list-content','aside_box',
  'ks-container','ks-content','settings-overlay','knowledge-layout',
  'faq-manager-wrapper','login-layout'];

function sendDrag(){
  try{window.webkit.messageHandlers.external.postMessage('drag')}
  catch(_){try{window.WailsInvoke('drag')}catch(e){}}
}

function isLayoutEl(el){
  for(var i=0;i<layoutClasses.length;i++){
    if(el.classList.contains(layoutClasses[i]))return true;
  }
  var tag=el.tagName;
  return tag==='BODY'||tag==='HTML';
}

function inTitlebar(el,y){
  // 原生标题栏模式下顶部由系统接管，不再做 Y 位置回退。
  if(!EMBEDDED_TITLEBAR)return false;
  if(y>TITLEBAR_H)return false;
  var node=el;
  while(node&&node instanceof Element){
    if(isLayoutEl(node))return true;
    node=node.parentElement;
  }
  return false;
}

function shouldDrag(el,y){
  if(!(el instanceof Element))return false;
  if(el.closest(noDragSel))return false;
  if(el.closest(dragSel))return true;
  if(inTitlebar(el,y))return true;
  return false;
}

window.addEventListener('mousedown',function(e){
  var target=e.target;
  if(target&&target.nodeType===Node.TEXT_NODE){
    target=target.parentElement;
  }
  if(e.button!==0||e.detail!==1)return;
  if(!shouldDrag(target,e.clientY))return;
  // The session title renames on double-click. Wait until the pointer
  // actually moves so that click still lands; other top-bar hits drag now.
  var defer=target.closest&&target.closest('.chat-header__title');
  if(!defer){
    e.preventDefault();
    sendDrag();
    return;
  }
  var x=e.clientX,y=e.clientY;
  function move(ev){
    if(Math.abs(ev.clientX-x)<4&&Math.abs(ev.clientY-y)<4)return;
    cleanup();
    sendDrag();
  }
  function up(){cleanup();}
  function cleanup(){
    window.removeEventListener('mousemove',move,true);
    window.removeEventListener('mouseup',up,true);
  }
  window.addEventListener('mousemove',move,true);
  window.addEventListener('mouseup',up,true);
},true);

// Intercept external link clicks and window.open so they open in the system browser
document.addEventListener('click',function(e){
  var el=e.target;
  while(el&&el.tagName!=='A')el=el.parentElement;
  if(!el||!el.href)return;
  var href=el.href;
  if(href.indexOf('http://')===0||href.indexOf('https://')===0){
    if(window.runtime&&window.runtime.BrowserOpenURL){
      e.preventDefault();
      e.stopPropagation();
      window.runtime.BrowserOpenURL(href);
    }
  }
},true);

var origOpen=window.open;
window.open=function(url){
  if(url&&(typeof url==='string')&&(url.indexOf('http://')===0||url.indexOf('https://')===0)){
    if(window.runtime&&window.runtime.BrowserOpenURL){
      window.runtime.BrowserOpenURL(url);
      return null;
    }
  }
  return origOpen.apply(window,arguments);
};
})();`

// wailsThemeSyncJS：与 index.html 首屏一致，在 DomReady 再跑一遍，覆盖 Ctrl+R 后 runtime 就绪时机
// OEM：告诉注入脚本当前标题栏形态，决定是否启用「顶部 40px 拖动」回退。
//
// HiddenInset 下 FullSizeContent=true，webview 铺满整个窗口，顶部 40px 没有
// 原生标题栏可拖，必须靠这个 Y 位置回退才有「拖空白处移动窗口」的体验。
// 若将来换成 TitleBarDefault（原生标题栏），把它改成 false，
// 否则内容区顶部会被误判成拖动区。
const desktopTitlebarModeJS = `try{window.__WK_EMBEDDED_TITLEBAR__=true}catch(e){}`

const wailsThemeSyncJS = `(function(){try{var t=localStorage.getItem('WeKnora_theme')||'light';if(t==='system')t=window.matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light';var bg=t==='dark'?'#181818':'#eee';document.documentElement.setAttribute('theme-mode',t);document.documentElement.style.background=bg;document.documentElement.style.minHeight='100%';document.documentElement.style.colorScheme=t==='dark'?'dark':'light';if(document.body){document.body.style.background=bg;document.body.style.minHeight='100%';}var w=window.runtime;if(!w)return;if(t==='dark'){if(w.WindowSetDarkTheme)w.WindowSetDarkTheme();if(w.WindowSetBackgroundColour)w.WindowSetBackgroundColour(24,24,24,255);}else{if(w.WindowSetLightTheme)w.WindowSetLightTheme();if(w.WindowSetBackgroundColour)w.WindowSetBackgroundColour(238,238,238,255);}}catch(e){}})()`

// ensureDesktopLiteEdition marks this process as Lite. cmd/desktop is the
// Lite app; packaged builds also inject this via ldflags. wails dev often
// does not, and auto-setup / the embedded SPA / capabilities still key off
// the string. Host sandbox is gated by the desktop build tag, not this.
func ensureDesktopLiteEdition() {
	handler.Edition = "lite"
}

// desktopWindowTitle 组装原生标题栏文案：「应用名 v版本号」。
//
// 版本号的三级回退（顺序即优先级）：
//  1. desktopVersion —— 由 desktop_webdir.go 里 go:embed 的 VERSION 文件填充，
//     编译期就进二进制，不依赖运行时 cwd，打包后也一定拿得到。
//  2. desktopAboutVersion() —— handler.Version（ldflags 注入）或运行时读 VERSION 文件，
//     兼容直接跑二进制调试的场景。
//
// desktopAppName 是产品名，同时决定 Windows/Linux 上的用户状态目录名
// （%APPDATA%\国研智研体）与日志文件名。
const desktopAppName = "国研智研体"

// 3. 都没有就只显示应用名，绝不出现 "vunknown" 这种丑陋文案。
func desktopWindowTitle() string {
	const appName = desktopAppName
	v := strings.TrimSpace(desktopVersion)
	if v == "" || v == "unknown" {
		v = strings.TrimSpace(desktopAboutVersion())
	}
	if v == "" || v == "unknown" {
		return appName
	}
	// VERSION 文件里可能已经带 v 前缀，避免出现 "vv0.9.0"
	return appName + " v" + strings.TrimPrefix(v, "v")
}

func main() {
	// 先把启动痕迹落盘：Windows 没有控制台，启动 panic 只表现为「双击没反应」。
	bootExec, bootErr := os.Executable()
	if bootErr == nil {
		bootLogHeader(bootExec)
		defer recoverBootPanic(bootExec)
	}

	ensureDesktopLiteEdition()

	// For macOS .app bundle, the working directory is usually "/" or the MacOS folder.
	// We need to change the working directory to the Resources folder where our configs are.
	execPath, errPath := os.Executable()
	configRoot, hasConfigRoot := "", false
	if errPath == nil {
		configRoot, hasConfigRoot = desktopConfigRoot(execPath)
	}
	// 必须是绝对路径：下面要 chdir 进去，再按 configRoot 找 .env。
	// 相对路径在 chdir 之后会指向「新 cwd 下的同名相对路径」，.env 读不到，
	// DB_DRIVER 就为空 → 启动 panic（Windows 上表现为双击没反应）。
	if hasConfigRoot {
		if abs, err := filepath.Abs(configRoot); err == nil {
			configRoot = abs
		}
	}
	if bootErr == nil {
		appendBootLog(bootExec, "config root=%q found=%v", configRoot, hasConfigRoot)
	}
	if hasConfigRoot {
		_ = os.Chdir(configRoot)
	}

	// Load .env explicitly for the desktop app so DB_DRIVER gets loaded.
	if err := loadDesktopEnv(configRoot, hasConfigRoot); err != nil {
		panic(err)
	}
	// wails dev picks up the repo .env, which may ask for Redis streams
	// without REDIS_ADDR. go-redis then dials localhost:6379 and aborts
	// startup. Lite has no Redis; keep the in-memory stream manager.
	if strings.TrimSpace(os.Getenv("REDIS_ADDR")) == "" &&
		strings.EqualFold(strings.TrimSpace(os.Getenv("STREAM_MANAGER_TYPE")), stream.TypeRedis) {
		_ = os.Setenv("STREAM_MANAGER_TYPE", stream.TypeMemory)
	}
	configureDesktopStorage(execPath)
	configureDesktopFileStorage(execPath)
	if bootErr == nil {
		appendBootLog(bootExec, "storage db_path=%q files=%q portable=%q",
			os.Getenv("DB_PATH"), os.Getenv("LOCAL_STORAGE_BASE_DIR"), os.Getenv("WK_PORTABLE_DIR"))
	}
	configureDesktopSSRFWhitelist()
	reportDesktopWriteSanity()

	// OEM 修复：让进程内的 Gin 也提供前端静态文件。
	//
	// 为什么必须这么做：Wails 的 AssetServer.Handler 指向一个把请求反向代理到
	// 本进程 Gin 的反代，所以窗口里的 "/" 实际打到 Gin。而 Gin 只有在设置了
	// WEKNORA_WEB_DIR 时才注册静态路由（internal/router/static.go:19-27）。
	// 不设的后果是连锁的：
	//   "/" 落到全局鉴权中间件 → 401 JSON
	//   → Wails 只在 200 && text/html 时才往 HTML 里注入 /wails/runtime.js
	//     （pkg/assetserver/assetserver.go 的 doRecord 判断）
	//   → window.go 不存在 → 前端 autoSetup() 拿不到 setup token
	//   → 永远停在 {"error":"Unauthorized: missing authentication"}
	// 桌面版的资源根就是上面 chdir 进去的 Resources 目录，前端放在 ./web。
	// 显式设置了 WEKNORA_WEB_DIR 时尊重外部配置（wails dev 会把前端放在别处）。
	if strings.TrimSpace(os.Getenv("WEKNORA_WEB_DIR")) == "" {
		if webDir, ok := resolveDesktopWebDir(hasConfigRoot); ok {
			_ = os.Setenv("WEKNORA_WEB_DIR", webDir)
		}
	}

	logger.ConfigureFromEnv()

	// Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}
	// Mute Gin's per-route registration spam; replaced by a single
	// summary printed after router build.
	runtime.SilenceGinRouteSpam()
	// Provision the signing key first so the startup banner reflects it
	// instead of warning about a key the desktop is about to create.
	if err := ensureDesktopSigningKey(); err != nil {
		panic(fmt.Sprintf("initialize desktop signing key: %v", err))
	}
	runtime.LogStartupEnv(context.Background())

	// Install prefs readers before BuildContainer. The host sandbox is created
	// during that call and keeps the loader functions it was given; decorating
	// the container afterwards does not rebuild it.
	container.SetHostApprovalModeLoader(LoadApprovalMode)
	container.SetHostProjectDirsLoader(LoadProjectDirs)

	// 授权服务必须在 BuildContainer 之前初始化：
	// BuildContainer 内部会 Provide 一个 *licenser.Service 的默认值（nil），
	// 而授权状态是容器装配时就该确定的——路由中间件在装配阶段拿到的
	// 实例会一直用下去，装配后再替换就需要额外的同步。
	//
	// 先把实例算出来放进容器可覆盖的位置：这里直接构造后通过
	// container.SetLicenseService 交给容器（见下方 provide 覆盖）。
	// 内置技能：管理端发布包里的技能在安装包里是纯资源目录，
	// 启动时按版本装进 ~/.weknora/skills，沙箱与 agent 才看得见。
	installBundledSkills(context.Background(), configRoot, hasConfigRoot)

	licenseService := setupDesktopLicense()
	container.SetLicenseServiceOverride(licenseService)

	// Build dependency injection container
	c := container.BuildContainer(runtime.GetContainer())

	// 桌面端把默认账号提升为系统管理员。
	//
	// 「全局运行时设置」页（SSRF 白名单、模型并发上限等）只对系统管理员
	// 可见。桌面端是单用户产品：注册出来的账号虽然是自己工作空间的 owner，
	// 却没有 is_system_admin 标志，于是用户在自己电脑上被告知「无权访问」。
	//
	// cmd/server 走的是 runStartupBootstrap，桌面端不经过那条路径，所以在
	// 这里直接调用同一套逻辑。只在「当前没有任何系统管理员」时才提升，
	// 幂等且非致命。
	promoteDesktopDefaultAdmin(c)

	// Initialize the WeKnora App struct
	app := NewApp()
	setupBytes := make([]byte, 32)
	if _, err := rand.Read(setupBytes); err != nil {
		panic("failed to generate desktop authentication capability")
	}
	app.setupToken = base64.RawURLEncoding.EncodeToString(setupBytes)
	handler.SetLiteSetupToken(app.setupToken)
	handler.SetHostProjectPicker(app.PickProjectDir)
	// 授权目录的列出与撤销。撤销必须走 HTTP，因为反向代理下的 SPA
	// 调不到 Wails 绑定；没有它用户就永远无法收回已授予的目录访问权。
	handler.SetHostProjectDirStore(LoadProjectDirs, removeApprovedProjectDir)
	// 「在文件管理器中打开」。同样必须走 HTTP：反向代理下的 SPA 拿不到
	// Wails 绑定，而 window.open("file://…") 会被 WKWebView 拒绝。
	handler.SetHostProjectDirRevealer(RevealApprovedProjectDir)
	// @ 引用项目文件：把磁盘上的文件登记成会话附件。边界校验必须复用
	// 同一份授权目录列表，所以由这里把 matcher 注入 session handler。
	sessionhandler.SetApprovedProjectPathMatcher(handler.MatchApprovedProjectPath)

	// Error channel to capture server startup errors
	serverErrCh := make(chan error, 1)

	// Run backend in a separate goroutine
	go func() {
		defer close(app.shutdownDone)
		err := c.Invoke(func(
			cfg *config.Config,
			router *gin.Engine,
			resourceCleaner interfaces.ResourceCleaner,
		) error {
			server := &http.Server{Handler: router}

			runtime.LogGinRouteCount(context.Background())

			// 127.0.0.1 + 固定端口（prefs 可覆盖），保证双击启动后地址稳定。
			addr := desktopBackendListenAddr()

			// 固定端口被别的东西长期占用时，不能让程序起不来——用户双击
			// 图标只期待「窗口出现」。重试几次仍失败就退回内核随机端口，
			// 页面内的所有请求都走 window.__WEKNORA_API_BASE__，端口变了
			// 前端也不需要改。
			listener, err := listenWithRetry(addr, 6, 300*time.Millisecond)
			if err != nil {
				logger.Warnf(context.Background(),
					"Preferred port %s unavailable (%v); falling back to an ephemeral port", addr, err)
				host, _, _ := net.SplitHostPort(addr)
				listener, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
				if err != nil {
					return fmt.Errorf("failed to start server: %v", err)
				}
			}

			tcpAddr := listener.Addr().(*net.TCPAddr)
			port := tcpAddr.Port
			app.listenPublic = LoadDesktopHTTPBindPublic()
			// Reverse proxy and webview API calls always use loopback; avoid 0.0.0.0 / [::] as dial target.
			app.backendURL = fmt.Sprintf("http://127.0.0.1:%d", port)
			app.apiLanBaseURL = ""
			if app.listenPublic {
				if ip := desktopPreferredLANIPv4(); ip != nil {
					app.apiLanBaseURL = fmt.Sprintf("http://%s:%d/api/v1", ip.String(), port)
				}
			}

			// Wails OnShutdown returns only after this goroutine closes
			// serverStopped. Closing the listener by hand makes Serve return
			// "use of closed network connection" instead of ErrServerClosed,
			// and the error path calls logger.Fatalf → os.Exit(1) before cleanup.
			serverStopped := make(chan struct{})
			go func() {
				defer close(serverStopped)
				<-app.shutdownCh
				logger.Infof(context.Background(), "Wails shutting down, stopping Go backend...")

				drainBudget, cleanupBudget := runtime.ShutdownBudgets(cfg.Server.ShutdownTimeout)
				shutdownCtx, cancel := context.WithTimeout(context.Background(), drainBudget)
				defer cancel()

				if err := server.Shutdown(shutdownCtx); err != nil {
					server.Close()
				}
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupBudget)
				defer cleanupCancel()
				if errs := resourceCleaner.Cleanup(cleanupCtx); len(errs) > 0 {
					logger.Errorf(context.Background(), "Errors occurred during resource cleanup: %v", errs)
				}
			}()

			// Also listen for OS signals just in case
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-signals
				select {
				case app.shutdownCh <- struct{}{}:
				default:
				}
			}()

			logger.Infof(context.Background(), "Server is running at %s (proxy -> %s)", tcpAddr.String(), app.backendURL)
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				return fmt.Errorf("server error: %v", err)
			}
			<-serverStopped
			return nil
		})
		if err != nil {
			serverErrCh <- err
			logger.Fatalf(context.Background(), "Failed to run backend: %v", err)
		}
	}()

	// Give the server a moment to start and determine its port
	time.Sleep(500 * time.Millisecond)

	// Create application with options
	// 应用菜单只在 macOS 创建：Linux/Windows 下 Wails 会把菜单渲染成窗口内的
	// 原生顶栏（应用名/View/Window），产品上不需要，直接不创建。
	var appMenu *menu.Menu
	if goruntime.GOOS == "darwin" {
		AppMenu := menu.NewMenu()
		FileMenu := AppMenu.AddSubmenu("国研智研体")
		FileMenu.AddText("About 国研智研体", keys.CmdOrCtrl("i"), func(_ *menu.CallbackData) {
			if app.ctx == nil {
				return
			}
			_, err := wailsruntime.MessageDialog(app.ctx, wailsruntime.MessageDialogOptions{
				Type:          wailsruntime.InfoDialog,
				Title:         "国研智研体",
				Message:       fmt.Sprintf("国研智研体 — 桌面版\n\n企业知识库与智能问答一体化平台。\n\nVersion %s\n© 2026", desktopAboutVersion()),
				Buttons:       []string{"OK"},
				DefaultButton: "OK",
			})
			if err != nil {
				logger.Warnf(context.Background(), "About dialog: %v", err)
			}
		})
		FileMenu.AddText("Check for Updates...", nil, func(_ *menu.CallbackData) {
			if app.ctx == nil {
				return
			}
			checkUpdate(app.ctx, desktopAboutVersion(), true, false)
		})
		FileMenu.AddSeparator()

		// 退出：先让后端正常收尾（停监听、关库），再结束进程。
		// 兜底看门狗 6 秒：正常收尾要等 drain / cleanup 预算，一旦某个环节卡住，
		// 用户看到的就是「按了 ⌘Q 没反应」。宁可硬退，也不要假死。
		quitApp := func(_ *menu.CallbackData) {
			go func() {
				time.AfterFunc(6*time.Second, func() { os.Exit(0) })
				app.shutdown(context.Background())
				os.Exit(0)
			}()
		}
		FileMenu.AddText("Quit", keys.CmdOrCtrl("q"), quitApp)

		AppMenu.Append(menu.EditMenu())

		ViewMenu := AppMenu.AddSubmenu("View")
		ViewMenu.AddText("Reload", keys.CmdOrCtrl("r"), func(_ *menu.CallbackData) {
			if app.ctx != nil {
				wailsruntime.EventsEmit(app.ctx, "app:reload")
			}
		})

		// Window 菜单：⌘M / ⌘W 这类快捷键不是窗口自己处理的。macOS 的路径是
		// NSApplication 收到按键 → 去 NSApp.mainMenu 里找 keyEquivalent 匹配的
		// 条目 → 找到才把 action 发出去。Wails 默认只给 App 与 Edit 两段菜单，
		// 少一段 Window，这两个键就一个 action 都匹配不到，表现成「死键」。
		WindowMenu := AppMenu.AddSubmenu("Window")
		WindowMenu.AddText("Minimize", keys.CmdOrCtrl("m"), func(_ *menu.CallbackData) {
			if app.ctx != nil {
				wailsruntime.WindowMinimise(app.ctx)
			}
		})
		WindowMenu.AddText("Zoom", nil, func(_ *menu.CallbackData) {
			if app.ctx == nil {
				return
			}
			if wailsruntime.WindowIsMaximised(app.ctx) {
				wailsruntime.WindowUnmaximise(app.ctx)
				return
			}
			wailsruntime.WindowMaximise(app.ctx)
		})
		WindowMenu.AddSeparator()
		// 关闭窗口：本应用单窗口、无托盘，窗口关掉后进程留在后台等于用户什么
		// 也看不到，所以 ⌘W 与 ⌘Q 走同一条收尾路径，避免出现隐形僵尸进程。
		WindowMenu.AddText("Close Window", keys.CmdOrCtrl("w"), quitApp)
		appMenu = AppMenu
	}

	// Wait for the backend URL to be set
	targetURL, _ := url.Parse(app.backendURL)
	proxy := desktopAPIProxy(targetURL)

	// Start Wails application
	// We use a Reverse Proxy to seamlessly proxy Wails' frontend to our Go backend
	err := wails.Run(&options.App{
		Title:         desktopWindowTitle(),
		Width:         1280,
		Height:        800,
		DisableResize: false,
		Menu:          appMenu,
		AssetServer: &assetserver.Options{
			Handler: proxy,
		},
		StartHidden: false, // Show window on startup
		OnStartup:   app.startup,
		OnDomReady: func(ctx context.Context) {
			// OEM：内嵌标题栏下系统标题栏被隐藏，版本号改由侧栏品牌区展示，
			// 这里把版本号注入页面，供 frontend/src/components/menu.vue 读取。
			wailsruntime.WindowSetTitle(ctx, desktopWindowTitle())
			wailsruntime.WindowExecJS(ctx, desktopTitlebarModeJS)
			if v := strings.TrimSpace(desktopVersion); v != "" && v != "unknown" {
				injectVersion := fmt.Sprintf(`try{window.__WK_APP_VERSION__=%s}catch(e){}`, strconv.Quote(strings.TrimPrefix(v, "v")))
				wailsruntime.WindowExecJS(ctx, injectVersion)
			}
			wailsruntime.WindowExecJS(ctx, wailsThemeSyncJS)
			wailsruntime.WindowExecJS(ctx, dragHandlerJS)
			// 注入真实 API 根路径（与 window.location.origin 不同）；无 Go 绑定时仍可显示。
			if u := strings.TrimSpace(app.backendURL); u != "" {
				apiRoot := strings.TrimRight(u, "/") + "/api/v1"
				inject := fmt.Sprintf(`try{window.__WEKNORA_API_BASE__=%s}catch(e){}`, strconv.Quote(apiRoot))
				wailsruntime.WindowExecJS(ctx, inject)
			}
			if lan := strings.TrimSpace(app.apiLanBaseURL); lan != "" {
				injectLan := fmt.Sprintf(`try{window.__WEKNORA_API_LAN_BASE__=%s}catch(e){}`, strconv.Quote(lan))
				wailsruntime.WindowExecJS(ctx, injectLan)
			}
		},
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		Linux: &linux.Options{
			Icon: desktopIcon,
		},
		Mac: &mac.Options{
			// OEM：回到内嵌标题栏（HiddenInset）。红黄绿按钮由系统绘制在窗口
			// 左上角、浮在 webview 之上，因此侧栏顶部必须预留一块空白给它，
			// 由 frontend/src/components/menu.vue 的
			// html.wails-desktop .aside_box { padding-top } 负责让位。
			// 不使用原生标题栏（TitleBarDefault）是为了保持无边框的极简观感；
			// 代价是应用名/版本号改由侧栏品牌区展示，不再出现在系统标题栏。
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		// Windows 是 GUI 子系统，println 无处可去：WebView2 运行时缺失时
		// wails.Run 就在这里返回，用户看到的就是「双击没反应」。落盘 + 弹窗。
		println("Error:", err.Error())
		appendBootLog(execPath, "wails.Run failed: %v", err)
		notifyBootFailure(execPath, err)
		os.Exit(1)
	}
}

// configureDesktopSSRFWhitelist 让桌面端可以直连本机（loopback）上运行的
// 模型服务，例如 ollama / vLLM / LM Studio 之类监听 127.0.0.1:xxxx 的
// OpenAI 兼容端点。
//
// 为什么桌面端要单独放开：
//
//	SSRF 防护针对的是「多租户服务端拿到用户填的 URL 就可能被诱导访问内网」，
//	而桌面端只有一个用户，127.0.0.1 就是他自己的电脑，本机上不存在「被
//	越权访问的内网资源」这一前提。
//
// 实现方式沿用仓库既有的 SSRF_WHITELIST_EXTRA 语义（见
// internal/utils/security.go 的 loadSSRFWhitelist）：它是「叠加」而非
// 「覆盖」，用户自己在设置里配的白名单不受影响。只在变量为空时写入默认值，
// 让操作员仍可用环境变量整体接管。
// promoteDesktopDefaultAdmin 确保桌面端的默认账号是系统管理员。
//
// 复用 config.AutomaticModelConfig 之外的既有约定：桌面端首次注册固定
// 使用 admin@weknora.local。提升动作交给 UserService，失败只记日志不阻断
// 启动——一个没有系统管理员的桌面端仍然能正常问答，只是看不到几个设置页。
func promoteDesktopDefaultAdmin(c *dig.Container) {
	ctx := context.Background()
	if err := c.Invoke(func(userSvc interfaces.UserService) {
		user, err := userSvc.GetUserByEmail(ctx, desktopDefaultAdminEmail)
		if err != nil || user == nil {
			// 首次启动时用户尚未注册，下次启动会再试一次。
			return
		}
		if user.IsSystemAdmin {
			return
		}
		// 已经有别的系统管理员时不抢权限，保持与 cmd/server 的语义一致。
		if _, total, err := userSvc.ListSystemAdmins(ctx, 0, 1); err == nil && total > 0 {
			return
		}
		user.IsSystemAdmin = true
		if err := userSvc.UpdateUser(ctx, user); err != nil {
			logger.Warnf(ctx, "[desktop] promote %s to system admin failed: %v", user.Email, err)
			return
		}
		logger.Infof(ctx, "[desktop] promoted %s (%s) to system admin", user.Username, user.Email)
	}); err != nil {
		logger.Warnf(ctx, "[desktop] system admin bootstrap unavailable: %v", err)
	}
}

// desktopDefaultAdminEmail 是桌面端首次注册时使用的默认账号邮箱。
const desktopDefaultAdminEmail = "admin@weknora.local"

func configureDesktopSSRFWhitelist() {
	if strings.TrimSpace(os.Getenv("SSRF_WHITELIST_EXTRA")) != "" {
		return
	}
	// IPv6 用不带方括号的裸地址，与 net.ParseIP / Hostname() 的返回一致。
	_ = os.Setenv("SSRF_WHITELIST_EXTRA", "127.0.0.1,localhost,::1")
}

func configureDesktopStorage(execPath string) {
	appSupportDir, err := desktopStateDir(execPath)
	if err != nil {
		logger.Warnf(context.Background(), "Failed to resolve desktop state dir: %v", err)
		return
	}

	// macOS 的上一版把数据放在 .app/Contents/Resources，搬一次；其它平台
	// 没有这段历史，跳过。
	if goruntime.GOOS == "darwin" && strings.Contains(execPath, ".app/Contents/MacOS") {
		legacyResourcesDir := filepath.Join(filepath.Dir(filepath.Dir(execPath)), "Resources")
		migrateLegacyDesktopData(legacyResourcesDir, filepath.Join(appSupportDir, "data"))
	}

	// .env 缺失/被安全软件删掉时 DB_DRIVER 会是空串，容器初始化会直接报
	// "unsupported database driver"。桌面版只支持 sqlite，这里补一个兜底。
	if strings.TrimSpace(os.Getenv("DB_DRIVER")) == "" {
		_ = os.Setenv("DB_DRIVER", "sqlite")
	}

	dbPath := resolveDesktopDataPath(os.Getenv("DB_PATH"), filepath.Join("data", "weknora.db"), appSupportDir)

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create desktop DB directory %s: %v", filepath.Dir(dbPath), err)
	}

	_ = os.Setenv("DB_PATH", dbPath)
}

// desktopStateDir is where every writable piece of desktop state lives.
//
// Windows 必须走这里：安装目录在 C:\Program Files 下是只读的，而打包进去的
// .env 写的是相对路径 ./data/weknora.db。进程 chdir 到安装目录后 sqlite 打不开
// 那个文件就 panic，Windows 上又没有控制台，用户看到的只是「双击没反应」。
// 所以非 macOS 平台一律把状态放到用户可写目录（%APPDATA%\国研智研体）。
func desktopStateDir(execPath string) (string, error) {
	if portable := strings.TrimSpace(os.Getenv("WK_PORTABLE_DIR")); portable != "" {
		return portable, nil
	}
	if goruntime.GOOS == "darwin" && strings.Contains(execPath, ".app/Contents/MacOS") {
		return defaultMacAppSupportDir(execPath)
	}
	configDir, err := desktopConfigDir()
	if err != nil {
		return "", err
	}
	if configDir == "" {
		return "", fmt.Errorf("cannot resolve user config dir")
	}
	// desktopConfigDir 是「配置根」，各种应用的子目录应该建在它下面。
	return filepath.Join(configDir, desktopAppName), nil
}

// configureDesktopFileStorage points Lite file uploads at ~/.weknora/data/files.
// wails dev is not always inside a packaged .app, so this runs for every
// desktop process. An explicit absolute directory is kept; the Docker default
// /data/files is not writable on macOS and is replaced. Uploads move over from
// wherever the previous build kept them.
func configureDesktopFileStorage(execPath string) {
	// 便携模式：上传文件同样落在便携目录，避免依赖 ~/.weknora。
	if portable := strings.TrimSpace(os.Getenv("WK_PORTABLE_DIR")); portable != "" {
		filesPath := filepath.Join(portable, "files")
		if err := os.MkdirAll(filesPath, 0o755); err != nil {
			logger.Warnf(context.Background(), "Failed to create portable files directory %s: %v", filesPath, err)
			return
		}
		_ = os.Setenv("LOCAL_STORAGE_BASE_DIR", filesPath)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Warnf(context.Background(), "Failed to resolve home dir for file storage: %v", err)
		return
	}
	filesPath := desktopLocalFilesDir(home)
	if stateDir, err := desktopStateDir(execPath); err == nil && goruntime.GOOS != "darwin" {
		// 非 macOS：跟随数据库一起放在用户可写目录，别再落到安装目录或
		// 家目录下的隐藏目录（企业环境里后者常被管控）。
		filesPath = filepath.Join(stateDir, "data", "files")
	}
	raw := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if !useDesktopFilesDir(raw) {
		return
	}
	if appSupport, err := defaultMacAppSupportDir(execPath); err == nil {
		migrateDesktopFiles(legacyDesktopFilesDir(raw, appSupport), filesPath)
	}
	if err := os.MkdirAll(filesPath, 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create desktop files directory %s: %v", filesPath, err)
		return
	}
	_ = os.Setenv("LOCAL_STORAGE_BASE_DIR", filesPath)
}

func desktopLocalFilesDir(home string) string {
	return filepath.Join(home, ".weknora", "data", "files")
}

func useDesktopFilesDir(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true
	}
	cleaned := filepath.Clean(trimmed)
	switch cleaned {
	case "/data/files", "data/files", filepath.Join(".", "data", "files"):
		return true
	default:
		return !filepath.IsAbs(cleaned)
	}
}

// legacyDesktopFilesDir is where the previous build stored uploads: raw
// resolved under the bundle's Application Support dir, or its data/files
// default when raw is empty or the absolute Docker default.
func legacyDesktopFilesDir(raw, appSupportDir string) string {
	if filepath.IsAbs(strings.TrimSpace(raw)) {
		raw = ""
	}
	return resolveDesktopDataPath(raw, filepath.Join("data", "files"), appSupportDir)
}

func migrateDesktopFiles(oldDir, newDir string) {
	if filepath.Clean(oldDir) == filepath.Clean(newDir) {
		return
	}
	info, err := os.Stat(oldDir)
	if err != nil || !info.IsDir() {
		return
	}
	if _, err := os.Stat(newDir); err == nil {
		logger.Warnf(context.Background(),
			"Desktop files already exist at %s; leaving %s in place", newDir, oldDir)
		return
	}
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create %s: %v", filepath.Dir(newDir), err)
		return
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		logger.Warnf(context.Background(), "Failed to migrate desktop files from %s to %s: %v", oldDir, newDir, err)
	}
}

func defaultMacAppSupportDir(execPath string) (string, error) {
	// 便携模式：设置 WK_PORTABLE_DIR 后所有可写状态（数据库、上传文件、prefs、
	// signing.key）都落在该目录下，不再依赖 ~/Library/Application Support。
	// 该目录不可写的环境（沙箱、企业管控、只读家目录）里，默认路径会让程序
	// 在启动阶段直接失败，因此这里优先认便携目录。
	if portable := strings.TrimSpace(os.Getenv("WK_PORTABLE_DIR")); portable != "" {
		return portable, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	appName := "国研智研体"
	if idx := strings.Index(execPath, ".app/Contents/MacOS"); idx >= 0 {
		bundleName := filepath.Base(execPath[:idx+4])
		if trimmed := strings.TrimSuffix(bundleName, ".app"); trimmed != "" {
			appName = trimmed
		}
	}

	return filepath.Join(homeDir, "Library", "Application Support", appName), nil
}

func resolveDesktopDataPath(rawPath, defaultRelativePath, appSupportDir string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		trimmed = defaultRelativePath
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	trimmed = strings.TrimPrefix(trimmed, "."+string(filepath.Separator))
	return filepath.Join(appSupportDir, filepath.Clean(trimmed))
}

// desktopAPIProxy forwards the webview to the loopback Gin server. Pairing
// needs the API listener as Host so the link cannot be aimed at another
// machine. Other routes keep the page Host (wails.localhost), which OIDC
// callbacks and embed origin checks still compare against.
func desktopAPIProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.SetXForwarded()
			if !isBrowserPairingRequest(r.In) {
				r.Out.Host = r.In.Host
			}
		},
	}
}

func isBrowserPairingRequest(req *http.Request) bool {
	return req != nil && req.URL != nil && req.Method == http.MethodPost && req.URL.Path == "/api/v1/me/browser"
}

// desktopBackendListenAddr returns the TCP address for the embedded Gin server (Wails desktop).
// Binds 127.0.0.1 by default, or 0.0.0.0 when http_bind_public is set in desktop-prefs.json.
// desktopDefaultHTTPPort 是双击启动时优先尝试的端口。
//
// 为什么需要它：原先无固定端口时监听 ":0"，由内核随机分配，每次启动
// 端口都不同。用户把地址存成书签、或者从别的设备访问，下次就打不开了。
// 选 8740 是因为它在用户端口段（1024-49151）内，且与本项目其他服务
// （licadmin 8091、OCR 若干）都不冲突。
//
// 端口被占不算错误：listenWithRetry 会先重试，仍失败则上层回落随机端口，
// 用户完全无感。
const desktopDefaultHTTPPort = 8740

func desktopBackendListenAddr() string {
	host := "127.0.0.1"
	if LoadDesktopHTTPBindPublic() {
		host = "0.0.0.0"
	}
	pref := LoadDesktopPrefsHTTPPort()
	if pref >= 1 && pref <= 65535 {
		return net.JoinHostPort(host, strconv.Itoa(pref))
	}
	// prefs 里没有固定端口时，优先用约定端口；被占用再由
	// listenWithRetry 的重试逻辑失败，最后落到系统的随机端口。
	// 这样「双击打开」每次拿到的地址是稳定的，书签和局域网访问不会失灵。
	return net.JoinHostPort(host, strconv.Itoa(desktopDefaultHTTPPort))
}

// desktopPreferredLANIPv4 picks a non-loopback IPv4 for “other devices on this network” URL hints.
func desktopPreferredLANIPv4() net.IP {
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, aerr := iface.Addrs()
			if aerr != nil {
				continue
			}
			for _, a := range addrs {
				ipNet, ok := a.(*net.IPNet)
				if !ok || ipNet.IP == nil {
					continue
				}
				ip4 := ipNet.IP.To4()
				if ip4 == nil || ip4.IsLoopback() {
					continue
				}
				if ip4.IsPrivate() {
					return ip4
				}
			}
		}
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, aerr := iface.Addrs()
			if aerr != nil {
				continue
			}
			for _, a := range addrs {
				ipNet, ok := a.(*net.IPNet)
				if !ok || ipNet.IP == nil {
					continue
				}
				ip4 := ipNet.IP.To4()
				if ip4 == nil || ip4.IsLoopback() || ip4.IsUnspecified() {
					continue
				}
				return ip4
			}
		}
	}
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return nil
	}
	defer conn.Close()
	la, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || la.IP == nil {
		return nil
	}
	return la.IP.To4()
}

func migrateLegacyDesktopData(resourcesDir, targetDataDir string) {
	legacyDataDir := filepath.Join(resourcesDir, "data")
	if info, err := os.Stat(legacyDataDir); err != nil || !info.IsDir() {
		return
	}
	if _, err := os.Stat(targetDataDir); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(targetDataDir), 0o755); err != nil {
		logger.Warnf(context.Background(), "Failed to create app support parent dir %s: %v", filepath.Dir(targetDataDir), err)
		return
	}
	if err := os.Rename(legacyDataDir, targetDataDir); err != nil {
		logger.Warnf(context.Background(), "Failed to migrate legacy desktop data from %s to %s: %v", legacyDataDir, targetDataDir, err)
		return
	}
	logger.Infof(context.Background(), "Migrated legacy desktop data to %s", targetDataDir)
}

func listenWithRetry(addr string, maxRetries int, baseDelay time.Duration) (net.Listener, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			return listener, nil
		}
		lastErr = err
		if i < maxRetries-1 {
			delay := baseDelay * time.Duration(1<<uint(i))
			if delay > 3*time.Second {
				delay = 3 * time.Second
			}
			time.Sleep(delay)
		}
	}
	return nil, lastErr
}

// reportDesktopWriteSanity 在启动时探测一次「本进程能不能写家目录」。
//
// 为什么需要它：macOS 上有两种完全不同的失败长得一模一样——
//   - 内核 Seatbelt（sandbox-exec）拒绝 → shell_exec 与技能安装全废
//   - 应用本身缺 TCC 授权 → 写 Desktop / 家目录报 operation not permitted
//
// 两者都会让技能安装、shell_exec、sandbox-exec 报同一句
// "operation not permitted"，日志里看不出区别，排查全靠猜。
//
// 这里在 ~/.weknora 下建一个一次性探针文件，把结论写成一行明确的日志，
// 让用户（和后续排障的人）一眼知道该修内核还是该授权。
// 探针用完即删，不写也不删用户任何数据。
func reportDesktopWriteSanity() {
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Warnf(context.Background(), "[desktop] write sanity: cannot resolve home dir: %v", err)
		return
	}

	stateDir := filepath.Join(home, ".weknora")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		logger.Warnf(context.Background(),
			"[desktop] write sanity: cannot create %s: %v — 技能安装与文件写入会失败，"+
				"请确认应用已获得该目录的访问权限（系统设置 → 隐私与安全性 → 完全磁盘访问）",
			stateDir, err)
		return
	}

	probe := filepath.Join(stateDir, ".write-sanity-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		logger.Warnf(context.Background(),
			"[desktop] write sanity: FAILED writing %s: %v — 本进程被系统拒绝写入家目录，"+
				"技能安装（.locks）与 shell_exec 都会报 operation not permitted",
			probe, err)
		return
	}
	_ = os.Remove(probe)
	logger.Infof(context.Background(), "[desktop] write sanity: home directory is writable (%s)", stateDir)
}
