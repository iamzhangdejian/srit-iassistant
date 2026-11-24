package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Tencent/WeKnora/internal/license/licadmin"
)

// serverBuildOptions 描述一次「把管理端组装起来」需要的输入。
//
// 抽成共享函数的原因很直接：命令行模式（-data/-port）与双击模式
// （-app-mode）原本各写一遍装配逻辑，双击版就漏掉了技能库与发布库——
// 打包出去的 .app 里「技能管理」「版本发布」两个视图全是 404。
// 装配只能有一份，漏配一次就是一个交付缺陷。
type serverBuildOptions struct {
	dataDir     string
	keyPath     string
	token       string
	allowRemote bool
	logPath     string
}

// builtServer 是装配结果；close 负责释放数据库连接。
type builtServer struct {
	srv       *licadmin.Server
	auth      *licadmin.AuthStore
	publicKey string
	close     func()
}

// buildServer 组装管理端：先应用待生效的恢复包，再打开数据库和各子库。
//
// 顺序是有意为之：恢复包要覆盖 licadmin.db，必须在 SQLite 建立连接之前
// 落到磁盘，否则开着 WAL 覆盖数据库会写坏文件。
func buildServer(opts serverBuildOptions) (*builtServer, error) {
	if err := ensureDataDir(opts.dataDir); err != nil {
		return nil, err
	}

	// 1) 上次上传的恢复包：在这里生效（进程刚起来，还没打开数据库）
	if kept, err := licadmin.ApplyPendingRestore(opts.dataDir, time.Now()); err != nil {
		return nil, fmt.Errorf("应用恢复包失败：%w", err)
	} else if kept != "" {
		fmt.Printf("已应用待生效的恢复包，原数据已备份到 %s\n", kept)
	}

	store := licadmin.NewStore(filepath.Join(opts.dataDir, "ledger.json"))
	svc, err := licadmin.NewService(licadmin.ServiceOptions{
		Store:          store,
		PrivateKeyPath: opts.keyPath,
	})
	if err != nil {
		return nil, fmt.Errorf("加载私钥失败：%w", err)
	}

	auth, err := licadmin.OpenAuthStore(filepath.Join(opts.dataDir, "licadmin.db"))
	if err != nil {
		return nil, fmt.Errorf("打开账号库失败：%w", err)
	}

	skills, err := licadmin.NewSkillStoreForData(auth.DB(), opts.dataDir)
	if err != nil {
		auth.Close()
		return nil, fmt.Errorf("初始化技能库失败：%w", err)
	}

	releases, err := licadmin.NewReleaseStoreForData(auth.DB(), opts.dataDir)
	if err != nil {
		auth.Close()
		return nil, fmt.Errorf("初始化发布库失败：%w", err)
	}

	tokens, err := licadmin.NewTokenStore(auth.DB())
	if err != nil {
		auth.Close()
		return nil, fmt.Errorf("初始化令牌库失败：%w", err)
	}

	srv := licadmin.NewServer(licadmin.ServerOptions{
		Service:     svc,
		Auth:        auth,
		Skills:      skills,
		Releases:    releases,
		Tokens:      tokens,
		Token:       opts.token,
		AllowRemote: opts.allowRemote,
		LogPath:     opts.logPath,
		DataDir:     opts.dataDir,
	})
	return &builtServer{
		srv:       srv,
		auth:      auth,
		publicKey: svc.PublicKeyBase64(),
		close:     func() { auth.Close() },
	}, nil
}

// ensureDataDir 建目录并给 0700：数据目录里有私钥，不能让同机其他用户读到。
func ensureDataDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("数据目录为空")
	}
	return os.MkdirAll(dir, 0o700)
}
