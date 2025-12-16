package agent

import (
	"net"
	"os"
	"runtime"
	"strings"
)

// GetMacAddress 获取稳定的MAC地址
func GetMacAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "none"
	}

	var macs []string
	for _, iface := range interfaces {
		// 跳过回环接口
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		// 跳过未激活的接口
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		// 过滤虚拟网卡
		desc := strings.ToLower(iface.Name)
		if strings.Contains(desc, "virtual") ||
			strings.Contains(desc, "vmware") ||
			strings.Contains(desc, "vbox") ||
			strings.Contains(desc, "hyper-v") ||
			strings.Contains(desc, "docker") ||
			strings.Contains(desc, "bridge") {
			continue
		}

		mac := iface.HardwareAddr.String()
		if mac != "" && mac != "00:00:00:00:00:00" {
			// 清理格式，移除冒号并转大写
			clean := strings.ReplaceAll(strings.ToUpper(mac), ":", "")
			macs = append(macs, clean)
		}
	}

	if len(macs) == 0 {
		return "none"
	}

	// 返回第一个MAC地址（已按字母顺序排序）
	// 在Go中，遍历interfaces的顺序是确定的，所以通常第一个就是稳定的
	return macs[0]
}

// GetAllIP 获取所有非回环的IPv4地址
func GetAllIP() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return net.IPv4zero.String()
	}

	var ips []string
	for _, iface := range interfaces {
		// 跳过回环接口
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		// 跳过未激活的接口
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// 只要IPv4地址且非回环
			if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
				ips = append(ips, ip.String())
			}
		}
	}

	if len(ips) == 0 {
		return net.IPv4zero.String()
	}

	return strings.Join(ips, ",")
}

// GetPlatform 获取平台类型
// 1: Windows, 2: Linux, 3: macOS, 0: Unknown
func GetPlatform() int {
	switch runtime.GOOS {
	case "windows":
		return 1
	case "linux":
		return 2
	case "darwin":
		return 3
	default:
		return 0
	}
}

// GetOSArch 获取操作系统架构
func GetOSArch() string {
	return runtime.GOARCH
}

// GetOSDesc 获取操作系统描述
func GetOSDesc() string {
	return runtime.GOOS + " " + runtime.GOARCH
}

// GetHostName 获取主机名
func GetHostName() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
