package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func main() {
	if len(os.Args) == 1 {
		// 无参数时，执行注册
		register()
	} else {
		// 有参数时，处理协议调用
		handleProtocol(os.Args[1])
	}
}

// handleProtocol 处理jike://协议调用
func handleProtocol(arg string) {
	if strings.HasPrefix(arg, "jike://remotedesk") {
		// 解析远程桌面协议: jike://remotedesk/<id>/<pwd>
		re := regexp.MustCompile(`jike://remotedesk/(?P<id>\d+)/?(?P<pwd>.*)`)
		matches := re.FindStringSubmatch(arg)
		if len(matches) > 0 {
			id := matches[1]
			pwd := ""
			if len(matches) > 2 {
				pwd = matches[2]
			}
			startRustDesk("--connect", id, pwd)
		}
	} else if strings.HasPrefix(arg, "jike://filetransfer") {
		// 解析文件传输协议: jike://filetransfer/<id>/<pwd>
		re := regexp.MustCompile(`jike://filetransfer/(?P<id>\d+)/?(?P<pwd>.*)`)
		matches := re.FindStringSubmatch(arg)
		if len(matches) > 0 {
			id := matches[1]
			pwd := ""
			if len(matches) > 2 {
				pwd = matches[2]
			}
			startRustDesk("--file-transfer", id, pwd)
		}
	}
}

// startRustDesk 启动RustDesk程序
func startRustDesk(mode, id, pwd string) {
	// 获取当前程序所在目录
	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("获取程序路径失败: %v\n", err)
		return
	}
	workDir := filepath.Dir(exePath)

	// 尝试多个可能的rustdesk.exe路径
	rustdeskPaths := []string{
		filepath.Join(workDir, "rustdesk.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "RustDesk", "rustdesk.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "RustDesk", "rustdesk.exe"),
	}

	var rustdeskPath string
	for _, path := range rustdeskPaths {
		if _, err := os.Stat(path); err == nil {
			rustdeskPath = path
			break
		}
	}

	if rustdeskPath == "" {
		fmt.Println("未找到rustdesk.exe")
		return
	}

	// 构建参数
	var args []string
	if mode == "--connect" {
		args = []string{mode, id}
		if pwd != "" {
			args = append(args, "--password", pwd)
		}
	} else if mode == "--file-transfer" {
		args = []string{mode, id}
		if pwd != "" {
			args = append(args, "--password", pwd)
		}
	}

	// 启动进程
	cmd := exec.Command(rustdeskPath, args...)
	cmd.Dir = workDir
	err = cmd.Start()
	if err != nil {
		fmt.Printf("启动RustDesk失败: %v\n", err)
	}
}

// register 注册jike://协议到Windows注册表
func register() {
	// 检查管理员权限
	if !isRunAsAdmin() {
		fmt.Println("请以管理员身份重新运行！")
		fmt.Println("按回车键退出...")
		fmt.Scanln()
		return
	}

	// 获取当前程序路径
	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("获取程序路径失败: %v\n", err)
		fmt.Println("按回车键退出...")
		fmt.Scanln()
		return
	}

	const protocolName = "jike"

	// 1. 创建主键并写入默认值和 URL Protocol
	key, _, err := registry.CreateKey(registry.CLASSES_ROOT, protocolName, registry.SET_VALUE)
	if err != nil {
		fmt.Printf("创建注册表主键失败: %v\n", err)
		fmt.Println("按回车键退出...")
		fmt.Scanln()
		return
	}
	defer key.Close()

	err = key.SetStringValue("", "Jike Remote Desktop Protocol")
	if err != nil {
		fmt.Printf("设置默认值失败: %v\n", err)
	}

	err = key.SetStringValue("URL Protocol", "")
	if err != nil {
		fmt.Printf("设置URL Protocol失败: %v\n", err)
	}

	// 2. 创建DefaultIcon子键
	iconKey, _, err := registry.CreateKey(registry.CLASSES_ROOT, protocolName+"\\DefaultIcon", registry.SET_VALUE)
	if err != nil {
		fmt.Printf("创建DefaultIcon键失败: %v\n", err)
	} else {
		defer iconKey.Close()
		iconKey.SetStringValue("", fmt.Sprintf("\"%s\",0", exePath))
	}

	// 3. 创建shell和open键（容器键）
	shellKey, _, err := registry.CreateKey(registry.CLASSES_ROOT, protocolName+"\\shell", registry.SET_VALUE)
	if err != nil {
		fmt.Printf("创建shell键失败: %v\n", err)
	} else {
		shellKey.Close()
	}

	openKey, _, err := registry.CreateKey(registry.CLASSES_ROOT, protocolName+"\\shell\\open", registry.SET_VALUE)
	if err != nil {
		fmt.Printf("创建open键失败: %v\n", err)
	} else {
		openKey.Close()
	}

	// 4. 创建command子键
	cmdKey, _, err := registry.CreateKey(registry.CLASSES_ROOT, protocolName+"\\shell\\open\\command", registry.SET_VALUE)
	if err != nil {
		fmt.Printf("创建command键失败: %v\n", err)
	} else {
		defer cmdKey.Close()
		cmdKey.SetStringValue("", fmt.Sprintf("\"%s\" \"%%1\"", exePath))
	}

	fmt.Println("系统已经设置完成！按回车键退出")
	fmt.Scanln()
}

// isRunAsAdmin 检查是否以管理员权限运行
func isRunAsAdmin() bool {
	var sid *windows.SID

	// 获取管理员组的SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	// 检查当前token是否包含管理员组
	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}

	return member
}
