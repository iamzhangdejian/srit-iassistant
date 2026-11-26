package licenser

import (
	"sync"
	"time"
)

// 导入授权码的限流参数。
//
// 为什么需要限流：Ed25519 验签是纯 CPU 操作，离线伪造不可行，但一个
// 已登录（或即便未登录，因为 /api/v1/license 在放行清单里）的客户端
// 可以每秒打几十次，把单核 CPU 吃满，让桌面端整体卡顿。这既是稳定性
// 问题，也让「试码」这种低成本攻击变得可行（虽然成功率极低）。
//
// 选 5 次/分钟的理由：正常用户输错一两次就会去核对，五分钟内试到第
// 六次基本可以判定是自动化行为。这个额度对真实用户完全够用。
const (
	licenseApplyBurst  = 5
	licenseApplyWindow = time.Minute
)

// applyLimiter 是固定窗口计数器。
//
// 不用令牌桶：本场景只关心「一分钟内最多 N 次」，固定窗口实现更简单、
// 更好解释，边界抖动（窗口交界处可能放过 2N 次）对 5 次/分钟的量级
// 没有实际影响。
type applyLimiter struct {
	mu       sync.Mutex
	windowAt time.Time
	count    int
	now      func() time.Time
}

func newApplyLimiter() *applyLimiter {
	return &applyLimiter{now: time.Now}
}

// allow 判断本次请求是否放行，并在放行时计数。
func (l *applyLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.windowAt.IsZero() || now.Sub(l.windowAt) >= licenseApplyWindow {
		l.windowAt = now
		l.count = 0
	}
	if l.count >= licenseApplyBurst {
		return false
	}
	l.count++
	return true
}

// retryAfter 返回距离窗口重置还要等多久，用于 Retry-After 头。
func (l *applyLimiter) retryAfter() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windowAt.IsZero() {
		return 0
	}
	remain := licenseApplyWindow - l.now().Sub(l.windowAt)
	if remain < 0 {
		return 0
	}
	return remain
}
