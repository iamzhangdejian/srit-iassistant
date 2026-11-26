本目录是 github.com/shoenig/go-m1cpu@v0.1.6 的修改版（vendored）。

修改内容：
- 删除 cgo 初始化与 IOKit/CoreFoundation 调用（受限环境下会 SIGSEGV）
- 主频/缓存类 API 返回 0；核心数与型号改用 sysctl 探测，失败退化

许可证：MPL-2.0，全文见本目录 LICENSE。修改版源码即本目录内容，随仓库提供。
原始项目：https://github.com/shoenig/go-m1cpu
