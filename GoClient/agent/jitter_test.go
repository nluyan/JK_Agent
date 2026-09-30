package agent

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestJitterDurationKeepsWithin30Percent(t *testing.T) {
	base := 10 * time.Second
	lower := time.Duration(float64(base) * 0.7)
	upper := time.Duration(float64(base) * 1.3)
	for i := 0; i < 200; i++ {
		got := jitterDuration(base)
		if got < lower || got > upper {
			t.Fatalf("jitterDuration(%v) = %v, 期望在 [%v, %v] 之间", base, got, lower, upper)
		}
	}
}

func TestJitterDurationNonPositiveReturnsBase(t *testing.T) {
	if got := jitterDuration(0); got != 0 {
		t.Fatalf("jitterDuration(0) = %v, 期望 0", got)
	}
	if got := jitterDuration(-time.Second); got != -time.Second {
		t.Fatalf("jitterDuration(-1s) = %v, 期望原样返回", got)
	}
}

func TestJitterDurationActuallyJitters(t *testing.T) {
	seen := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		seen[jitterDuration(time.Minute)] = true
	}
	if len(seen) < 2 {
		t.Fatal("jitterDuration 未产生抖动，50次取值完全相同")
	}
}

func TestNextRetryDelayDoublesUntilCap(t *testing.T) {
	delay := initialRetryDelay
	if delay >= maxRetryDelay {
		t.Fatalf("初始退避间隔 %v 不应大于等于上限 %v", delay, maxRetryDelay)
	}
	for i := 0; i < 20; i++ {
		delay = nextRetryDelay(delay)
		if delay > maxRetryDelay {
			t.Fatalf("退避间隔 %v 超过上限 %v", delay, maxRetryDelay)
		}
	}
	if delay != maxRetryDelay {
		t.Fatalf("多次退避后应停在上限 %v, 实际 %v", maxRetryDelay, delay)
	}
}

func TestStartupDelayWithinJitter(t *testing.T) {
	a := &Agent{startupJitter: 5 * time.Second}
	for i := 0; i < 100; i++ {
		got := a.startupDelay()
		if got < 0 || got > 5*time.Second {
			t.Fatalf("startupDelay() = %v, 期望在 [0, 5s] 之间", got)
		}
	}
}

func TestStartupDelayDisabledByDefault(t *testing.T) {
	a := &Agent{}
	if got := a.startupDelay(); got != 0 {
		t.Fatalf("未设置错峰时 startupDelay() = %v, 期望 0", got)
	}
}

func TestShouldResetRetryDelayAfterStableConnection(t *testing.T) {
	if !shouldResetRetryDelay(errConnectionClosed, 2*time.Minute) {
		t.Fatal("连接稳定保持后断开，应重置退避间隔")
	}
}

func TestShouldResetRetryDelayAtThreshold(t *testing.T) {
	if !shouldResetRetryDelay(errConnectionClosed, stableConnectionDuration) {
		t.Fatal("连接时长恰好等于阈值时应重置退避间隔")
	}
}

func TestShouldResetRetryDelayOnWrappedError(t *testing.T) {
	wrapped := fmt.Errorf("外层包装: %w", errConnectionClosed)
	if !shouldResetRetryDelay(wrapped, 5*time.Minute) {
		t.Fatal("包装后的连接关闭错误也应被识别")
	}
}

func TestShouldNotResetRetryDelayWhenFlapping(t *testing.T) {
	if shouldResetRetryDelay(errConnectionClosed, 10*time.Second) {
		t.Fatal("刚连上就断开的抖动场景不应重置退避间隔")
	}
}

func TestShouldNotResetRetryDelayWhenNeverConnected(t *testing.T) {
	if shouldResetRetryDelay(errors.New("服务器不可连接"), time.Hour) {
		t.Fatal("从未连接成功时不应重置退避间隔")
	}
}
