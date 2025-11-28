//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"jkagent/goclient/agent"
	"jkagent/goclient/config"
)

func main() {
	fmt.Println("=== JikeAgent 基础功能测试 ===")
	fmt.Println()

	// 测试系统信息收集
	fmt.Println("1. 系统信息:")
	fmt.Printf("   MAC 地址: %s\n", agent.GetMacAddress())
	fmt.Printf("   IP 地址: %s\n", agent.GetAllIP())
	fmt.Printf("   平台: %d\n", agent.GetPlatform())
	fmt.Printf("   架构: %s\n", agent.GetOSArch())
	fmt.Printf("   OS 描述: %s\n", agent.GetOSDesc())
	fmt.Printf("   版本: %s\n", config.Default.Version)
	fmt.Println()

	// 测试 PowerShell 脚本执行
	fmt.Println("2. PowerShell 脚本执行测试:")
	testScript := `
Write-Output "Hello from PowerShell!"
Write-Output "当前时间: $(Get-Date)"
Write-Output "计算机名: $env:COMPUTERNAME"
`
	result := agent.ExecuteScriptNatively(testScript)
	fmt.Printf("   执行结果:\n%s\n", result)
	fmt.Println()

	fmt.Println("=== 测试完成 ===")
}
