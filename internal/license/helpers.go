package license

import (
	"errors"
	"strings"
)

// trimSpace 是 strings.TrimSpace 的别名，单独抽出来是为了让 state.go 的
// 判定逻辑读起来不被标准库包名打断（那个文件里字符串处理只此一处）。
func trimSpace(s string) string { return strings.TrimSpace(s) }

// isErr 判断 err 链上是否包含 target。
//
// 用 errors.Is 而不是 == ：Decode 里的错误都是 fmt.Errorf("...: %w", ...)
// 包装过的，直接比较会全部落到 default 分支。
func isErr(err, target error) bool { return errors.Is(err, target) }
