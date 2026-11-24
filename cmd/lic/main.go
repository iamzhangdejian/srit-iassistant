// Command lic 是授权码的运维命令行：生成密钥、签发授权码、校验授权码。
//
// 真正的实现放在 internal/license/liccmd，这里只做一层极薄的入口，
// 好处是同一份逻辑也能被 cmd/licadmin 的网页端直接调用（比如「验签自检」
// 用的一定是同一套解析代码，不会出现网页说有效、客户端说无效的分裂）。
package main

import (
	"os"

	"github.com/Tencent/WeKnora/internal/license/liccmd"
)

func main() {
	os.Exit(liccmd.Run(os.Args[1:]))
}
