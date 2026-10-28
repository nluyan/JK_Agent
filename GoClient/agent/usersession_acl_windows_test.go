//go:build windows

package agent

import (
	"os"
	"testing"
)

// grantUsersModifyOnDir 曾经把 StringToSid 与 ACLFromEntries 返回的 Go 堆指针交给
// LocalFree，进程会以 STATUS_HEAP_CORRUPTION(0xC0000374) 静默死亡：SCM 只报"意外停止"，
// 既不生成 WER 报告，服务模式下 runtime 的 fatal 文本也无处可去。
// 这里在真实目录上跑一遍，再次误用 LocalFree 时测试进程会直接崩溃而不是普通断言失败。
func TestGrantUsersModifyOnDirKeepsProcessAlive(t *testing.T) {
	dir := t.TempDir()

	if err := grantUsersModifyOnDir(dir); err != nil {
		t.Skipf("当前账户无权修改 %s 的 ACL: %v", dir, err)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("授权后目录不可访问: %v", err)
	}
}
