//go:build noduckdb || !cgo

package container

import "database/sql"

// NewDuckDB 是 Windows 构建（-tags noduckdb）或未启用 cgo 时的占位实现。
//
// 原因：duckdb-go-bindings 的 Windows 预编译静态库是用 clang 的「模拟 TLS」
// (-femulated-tls) 编出来的，链接期需要 __emutls_v.* 控制块与 __emutls_get_address
// 运行时；而 UCRT64 的 GCC 用的是原生 TLS，libgcc 里没有 emutls 运行时，
// 两者无法直接链接。因此 Windows 桌面版不编译 DuckDB，依赖它的两个功能
// （对话内的 Excel 数据分析、xlsx 表格文档摄取）在该平台不可用，
// 其余功能（含 xlsx 预览，走前端 SheetJS）不受影响。
//
// 返回 nil 而不是报错，是为了让 DI 容器仍能完成装配；调用方需判空，
// 并在需要时给出「当前平台构建不含 DuckDB」的明确提示。
func NewDuckDB() (*sql.DB, error) {
	return nil, nil
}
