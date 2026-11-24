package licenser

import (
	"testing"
	"time"
)

// 固定窗口限流的基本行为：额度内放行，超额度拒绝。
func TestApplyLimiterBurstThenBlock(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	l := &applyLimiter{now: func() time.Time { return now }}

	for i := 0; i < licenseApplyBurst; i++ {
		if !l.allow() {
			t.Fatalf("request %d within burst must be allowed", i+1)
		}
	}
	if l.allow() {
		t.Fatal("request beyond burst must be rejected")
	}
}

// 窗口滑动后额度恢复。
func TestApplyLimiterWindowResets(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	l := &applyLimiter{now: func() time.Time { return now }}

	for i := 0; i < licenseApplyBurst; i++ {
		l.allow()
	}
	if l.allow() {
		t.Fatal("should be blocked before window reset")
	}

	now = now.Add(licenseApplyWindow + time.Second)
	if !l.allow() {
		t.Fatal("must be allowed again after window reset")
	}
}

// retryAfter 必须在窗口内给出正数，窗口外给出 0。
func TestApplyLimiterRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	l := &applyLimiter{now: func() time.Time { return now }}

	if got := l.retryAfter(); got != 0 {
		t.Fatalf("no window yet, retryAfter = %v, want 0", got)
	}
	l.allow()
	if got := l.retryAfter(); got <= 0 || got > licenseApplyWindow {
		t.Fatalf("retryAfter = %v, want (0, %v]", got, licenseApplyWindow)
	}

	now = now.Add(licenseApplyWindow + time.Second)
	if got := l.retryAfter(); got != 0 {
		t.Fatalf("after window, retryAfter = %v, want 0", got)
	}
}
