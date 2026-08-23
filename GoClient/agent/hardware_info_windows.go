//go:build windows

package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/StackExchange/wmi"
)

// The inventory intentionally uses the same top-level names as the existing
// PowerShell collector, so callers can switch transport without changing the
// CMDB schema.
type hardwareInfo struct {
	CollectionTime string      `json:"采集时间"`
	System         interface{} `json:"系统信息"`
	Computer       interface{} `json:"硬件基础信息"`
	Processor      interface{} `json:"处理器"`
	Memory         interface{} `json:"内存"`
	Disks          interface{} `json:"硬盘"`
	RAID           interface{} `json:"阵列"`
	GPU            interface{} `json:"显卡"`
	Audio          interface{} `json:"声卡"`
	NIC            interface{} `json:"网卡"`
	Network        interface{} `json:"网络适配器信息"`
	Motherboard    interface{} `json:"主板"`
	Power          interface{} `json:"电源"`
}

type operatingSystemWMI struct {
	Caption          string
	Version          string
	InstallDate      string
	LastBootUpTime   string
	WindowsDirectory string
	SerialNumber     string
}

type computerSystemWMI struct {
	Name                      string
	Manufacturer              string
	Model                     string
	SystemType                string
	TotalPhysicalMemory       uint64
	NumberOfLogicalProcessors uint32
	NumberOfProcessors        uint32
}

type processorWMI struct {
	Name                      string
	NumberOfCores             uint32
	NumberOfLogicalProcessors uint32
	MaxClockSpeed             uint32
	SocketDesignation         string
	Manufacturer              string
	Architecture              uint16
}

type memoryWMI struct {
	Capacity     uint64
	Manufacturer string
	PartNumber   string
	Speed        uint32
}

type diskWMI struct {
	Index         uint32
	Model         string
	Manufacturer  string
	InterfaceType string
	Size          uint64
	SerialNumber  string
	MediaType     string
}

type partitionWMI struct {
	DiskIndex   uint32
	Index       uint32
	Size        uint64
	Type        string
	Description string
	DeviceID    string
}

type logicalDiskWMI struct {
	DeviceID    string
	VolumeName  string
	Size        uint64
	FileSystem  string
	Description string
}

type videoWMI struct {
	Name          string
	Manufacturer  string
	AdapterRAM    uint32
	DriverVersion string
	PNPDeviceID   string
}

type displayWMI struct {
	DeviceName string
	PelsWidth  uint32
	PelsHeight uint32
}

type soundWMI struct {
	Name         string
	Manufacturer string
	Status       string
	PNPDeviceID  string
}

type networkAdapterWMI struct {
	Name                string
	NetConnectionID     string
	Description         string
	Manufacturer        string
	MACAddress          string
	NetConnectionStatus uint16
	Speed               uint64
	Index               uint32
	PNPDeviceID         string
}

type networkConfigWMI struct {
	Description          string
	MACAddress           string
	IPAddress            []string
	IPSubnet             []string
	DefaultIPGateway     []string
	DNSServerSearchOrder []string
	DHCPEnabled          bool
	IPEnabled            bool
}

type baseBoardWMI struct {
	Manufacturer string
	Product      string
	Model        string
	SerialNumber string
	Version      string
}

type biosWMI struct {
	Manufacturer       string
	SMBIOSBIOSVersion  string
	Version            string
	ReleaseDate        string
	SMBIOSMajorVersion uint16
	SMBIOSMinorVersion uint16
	SerialNumber       string
}

type batteryWMI struct {
	Name               string
	Manufacturer       string
	DesignCapacity     uint32
	FullChargeCapacity uint32
	BatteryStatus      uint16
	EstimatedRunTime   uint32
}

func queryHardware[T any](query string, dst *[]T) error {
	return wmi.Query(query, dst)
}

func firstHardware[T any](query string) (*T, error) {
	var rows []T
	if err := queryHardware(query, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func textOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func roundGB(bytes uint64) float64 {
	return math.Round(float64(bytes)/(1024*1024*1024)*100) / 100
}

func dmtfDate(value string, withTime bool) string {
	value = strings.TrimSpace(value)
	if len(value) < 14 {
		return textOr(value, "未知")
	}
	layout := "20060102150405"
	parsed, err := time.ParseInLocation(layout, value[:14], time.Local)
	if err != nil {
		return textOr(value, "未知")
	}
	if withTime {
		return parsed.Format("2006-01-02 15:04:05")
	}
	return parsed.Format("2006-01-02")
}

func querySystemInfo() interface{} {
	row, err := firstHardware[operatingSystemWMI]("SELECT Caption, Version, InstallDate, LastBootUpTime, WindowsDirectory, SerialNumber FROM Win32_OperatingSystem")
	if err != nil || row == nil {
		return nil
	}
	status := "未激活"
	if textOr(row.SerialNumber, "") != "" && !strings.EqualFold(strings.TrimSpace(row.SerialNumber), "To Be Filled By O.E.M.") {
		status = "已激活"
	}
	return map[string]interface{}{
		"操作系统":   textOr(row.Caption, "未知") + " (" + textOr(row.Version, "未知") + ")",
		"安装日期":   dmtfDate(row.InstallDate, false),
		"系统启动时间": dmtfDate(row.LastBootUpTime, true),
		"系统目录":   textOr(row.WindowsDirectory, "未知"),
		"是否激活":   status,
	}
}

func queryComputerInfo() interface{} {
	row, err := firstHardware[computerSystemWMI]("SELECT Name, Manufacturer, Model, SystemType, TotalPhysicalMemory, NumberOfLogicalProcessors, NumberOfProcessors FROM Win32_ComputerSystem")
	if err != nil || row == nil {
		return nil
	}
	return map[string]interface{}{
		"设备名称": textOr(row.Name, "未知"), "制造商": textOr(row.Manufacturer, "未知制造商"),
		"型号": textOr(row.Model, "未知型号"), "系统类型": textOr(row.SystemType, "未知"),
		"物理内存_GB": roundGB(row.TotalPhysicalMemory), "逻辑处理器数": row.NumberOfLogicalProcessors,
		"物理处理器数": row.NumberOfProcessors,
	}
}

func queryProcessorInfo() interface{} {
	row, err := firstHardware[processorWMI]("SELECT Name, NumberOfCores, NumberOfLogicalProcessors, MaxClockSpeed, SocketDesignation, Manufacturer, Architecture FROM Win32_Processor")
	if err != nil || row == nil {
		return nil
	}
	arch := map[uint16]string{0: "x86", 9: "x64", 12: "ARM"}[row.Architecture]
	if arch == "" {
		arch = "未知"
	}
	return map[string]interface{}{"处理器名称": textOr(row.Name, "未知处理器"), "核心数": row.NumberOfCores, "线程数": row.NumberOfLogicalProcessors, "基础频率_GHz": math.Round(float64(row.MaxClockSpeed)/1000*100) / 100, "插座类型": textOr(row.SocketDesignation, "未知"), "制造商": textOr(row.Manufacturer, "未知制造商"), "架构": arch}
}

func queryMemoryInfo() interface{} {
	var rows []memoryWMI
	if err := queryHardware("SELECT Capacity, Manufacturer, PartNumber, Speed FROM Win32_PhysicalMemory", &rows); err != nil {
		return nil
	}
	var total uint64
	details := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Capacity == 0 {
			continue
		}
		total += row.Capacity
		details = append(details, fmt.Sprintf("%s %.2fGB (%dMHz) - %s", textOr(row.Manufacturer, "未知品牌"), float64(row.Capacity)/(1024*1024*1024), row.Speed, textOr(row.PartNumber, "无型号")))
	}
	detail := "无已安装内存模块"
	if len(details) > 0 {
		detail = strings.Join(details, "; ")
	}
	return map[string]interface{}{"总内存容量_GB": roundGB(total), "已占用插槽数": len(details), "内存详情": detail}
}

func queryDiskInfo() interface{} {
	var disks []diskWMI
	if err := queryHardware("SELECT Index, Model, Manufacturer, InterfaceType, Size, SerialNumber, MediaType FROM Win32_DiskDrive", &disks); err != nil {
		return nil
	}
	var partitions []partitionWMI
	_ = queryHardware("SELECT DiskIndex, Index FROM Win32_DiskPartition", &partitions)
	counts := make(map[uint32]int)
	for _, p := range partitions {
		counts[p.DiskIndex]++
	}
	list := make([]map[string]interface{}, 0, len(disks))
	for _, disk := range disks {
		if disk.Size == 0 || (disk.MediaType != "" && !strings.Contains(strings.ToLower(disk.MediaType), "fixed")) {
			continue
		}
		list = append(list, map[string]interface{}{"磁盘序号": disk.Index + 1, "制造商": textOr(disk.Manufacturer, "未知制造商"), "磁盘型号": textOr(disk.Model, "未知型号"), "接口类型": textOr(disk.InterfaceType, "未知接口"), "总容量_GB": roundGB(disk.Size), "序列号": textOr(disk.SerialNumber, "无序列号"), "分区数量": counts[disk.Index]})
	}
	return map[string]interface{}{"total": len(list), "list": list}
}

func queryRAIDInfo() interface{} {
	var volumes []logicalDiskWMI
	var partitions []partitionWMI
	_ = queryHardware("SELECT DeviceID, VolumeName, Size, FileSystem, Description FROM Win32_LogicalDisk", &volumes)
	_ = queryHardware("SELECT Size, Type, Description, DeviceID FROM Win32_DiskPartition", &partitions)
	list := make([]map[string]interface{}, 0)
	for _, v := range volumes {
		if v.Size == 0 || !strings.Contains(strings.ToLower(v.Description), "raid") {
			continue
		}
		list = append(list, map[string]interface{}{"类型": "逻辑RAID卷", "驱动器号": v.DeviceID, "卷标": textOr(v.VolumeName, "无卷标"), "总容量_GB": roundGB(v.Size), "文件系统": textOr(v.FileSystem, "未知"), "描述": v.Description})
	}
	for _, p := range partitions {
		if strings.Contains(strings.ToLower(p.Type+" "+p.Description), "raid") {
			list = append(list, map[string]interface{}{"类型": "物理RAID分区", "驱动器号": "无", "卷标": "无", "总容量_GB": roundGB(p.Size), "文件系统": "未知", "描述": p.Description})
		}
	}
	details := interface{}("未检测到RAID配置")
	if len(list) > 0 {
		details = list
	}
	return map[string]interface{}{"RAID设备总数": len(list), "RAID详情列表": details}
}

func queryGPUInfo() interface{} {
	var rows []videoWMI
	if err := queryHardware("SELECT Name, Manufacturer, AdapterRAM, DriverVersion, PNPDeviceID FROM Win32_VideoController", &rows); err != nil {
		return nil
	}
	var displays []displayWMI
	_ = queryHardware("SELECT DeviceName, PelsWidth, PelsHeight FROM Win32_DisplayConfiguration", &displays)
	list := make([]map[string]interface{}, 0, len(rows))
	for i, row := range rows {
		resolution := "未知"
		if i < len(displays) && displays[i].PelsWidth > 0 {
			resolution = fmt.Sprintf("%dx%d", displays[i].PelsWidth, displays[i].PelsHeight)
		}
		bus := "未知"
		id := strings.ToUpper(row.PNPDeviceID)
		if strings.Contains(id, "PCI") {
			bus = "PCI/PCIe"
		} else if strings.Contains(id, "USB") {
			bus = "USB"
		} else if id != "" {
			bus = "集成"
		}
		list = append(list, map[string]interface{}{"显卡序号": i + 1, "显卡名称": textOr(row.Name, "未知显卡"), "制造商": textOr(row.Manufacturer, "未知制造商"), "显存容量_MB": uint64(row.AdapterRAM) / (1024 * 1024), "分辨率": resolution, "驱动版本": textOr(row.DriverVersion, "未知"), "接口类型": bus})
	}
	return map[string]interface{}{"total": len(list), "list": list}
}

func queryAudioInfo() interface{} {
	var rows []soundWMI
	if err := queryHardware("SELECT Name, Manufacturer, Status, PNPDeviceID FROM Win32_SoundDevice", &rows); err != nil {
		return nil
	}
	list := make([]map[string]interface{}, 0, len(rows))
	for i, row := range rows {
		if row.Status != "" && !strings.EqualFold(row.Status, "OK") {
			continue
		}
		kind := "音频设备"
		lower := strings.ToLower(row.Name)
		if strings.Contains(lower, "microphone") || strings.Contains(row.Name, "麦克风") {
			kind = "音频输入设备"
		} else if strings.Contains(lower, "speaker") || strings.Contains(lower, "headphone") || strings.Contains(row.Name, "扬声器") {
			kind = "音频输出设备"
		}
		list = append(list, map[string]interface{}{"设备序号": i + 1, "设备名称": textOr(row.Name, "未知音频设备"), "设备类型": kind, "制造商": textOr(row.Manufacturer, "未知制造商"), "状态": textOr(row.Status, "未知"), "PNP设备ID": textOr(row.PNPDeviceID, "无")})
	}
	return map[string]interface{}{"total": len(list), "list": list}
}

func queryNICInfo() interface{} {
	var rows []networkAdapterWMI
	if err := queryHardware("SELECT Name, NetConnectionID, Description, Manufacturer, MACAddress, NetConnectionStatus, Speed, Index, PNPDeviceID FROM Win32_NetworkAdapter", &rows); err != nil {
		return nil
	}
	list := make([]map[string]interface{}, 0, len(rows))
	for i, row := range rows {
		if strings.TrimSpace(row.Name) == "" && strings.TrimSpace(row.Description) == "" {
			continue
		}
		kind := "其他网卡"
		lower := strings.ToLower(row.Description + " " + row.Name)
		if strings.Contains(lower, "wireless") || strings.Contains(lower, "wi-fi") {
			kind = "无线网卡"
		} else if strings.Contains(lower, "bluetooth") {
			kind = "蓝牙网卡"
		} else if strings.Contains(lower, "ethernet") {
			kind = "以太网网卡"
		} else if strings.Contains(lower, "virtual") {
			kind = "虚拟网卡"
		}
		status := "未连接"
		if row.NetConnectionStatus == 2 {
			status = "已连接"
		}
		speed := "0 bps"
		if row.Speed > 0 {
			speed = fmt.Sprintf("%d bps", row.Speed)
		}
		list = append(list, map[string]interface{}{"网卡序号": i + 1, "网卡名称": textOr(row.NetConnectionID, row.Name), "接口描述": textOr(row.Description, "未知接口"), "网卡类型": kind, "制造商": textOr(row.Manufacturer, "未知制造商"), "MAC地址": textOr(row.MACAddress, "未分配"), "状态": status, "链接速度": speed, "ifIndex": row.Index, "PNP设备ID": textOr(row.PNPDeviceID, "无")})
	}
	return map[string]interface{}{"total": len(list), "list": list}
}

func queryNetworkInfo() interface{} {
	var rows []networkConfigWMI
	if err := queryHardware("SELECT Description, MACAddress, IPAddress, IPSubnet, DefaultIPGateway, DNSServerSearchOrder, DHCPEnabled, IPEnabled FROM Win32_NetworkAdapterConfiguration", &rows); err != nil {
		return nil
	}
	list := make([]map[string]interface{}, 0, len(rows))
	join := func(v []string, fallback string) string {
		if len(v) == 0 {
			return fallback
		}
		return strings.Join(v, "; ")
	}
	for _, row := range rows {
		if !row.IPEnabled {
			continue
		}
		list = append(list, map[string]interface{}{"适配器名称": textOr(row.Description, "未知适配器"), "MAC地址": textOr(row.MACAddress, "未分配"), "IP地址": join(row.IPAddress, "无IP"), "子网掩码": join(row.IPSubnet, "无子网掩码"), "网关": join(row.DefaultIPGateway, "无网关"), "DNS服务器": join(row.DNSServerSearchOrder, "无DNS"), "DHCP状态": map[bool]string{true: "启用", false: "禁用"}[row.DHCPEnabled]})
	}
	return list
}

func queryMotherboardInfo() interface{} {
	board, boardErr := firstHardware[baseBoardWMI]("SELECT Manufacturer, Product, Model, SerialNumber, Version FROM Win32_BaseBoard")
	bios, biosErr := firstHardware[biosWMI]("SELECT Manufacturer, SMBIOSBIOSVersion, Version, ReleaseDate, SMBIOSMajorVersion, SMBIOSMinorVersion, SerialNumber FROM Win32_BIOS")
	if boardErr != nil && biosErr != nil {
		return nil
	}
	result := map[string]interface{}{"主板制造商": "未知制造商", "主板型号": "未知型号", "主板序列号": "无序列号", "主板版本": "未知版本", "BIOS制造商": "未知制造商", "BIOS版本": "未知版本", "BIOS发布日期": "未知日期", "SMBIOS版本": "未知", "系统UUID": "未知"}
	if board != nil {
		result["主板制造商"] = textOr(board.Manufacturer, "未知制造商")
		result["主板型号"] = textOr(board.Product, textOr(board.Model, "未知型号"))
		serial := textOr(board.SerialNumber, "无序列号")
		if strings.Contains(strings.ToLower(serial), "to be filled") || strings.EqualFold(serial, "default string") {
			serial = "无序列号"
		}
		result["主板序列号"] = serial
		result["主板版本"] = textOr(board.Version, "未知版本")
	}
	if bios != nil {
		result["BIOS制造商"] = textOr(bios.Manufacturer, "未知制造商")
		result["BIOS版本"] = textOr(bios.SMBIOSBIOSVersion, textOr(bios.Version, "未知版本"))
		result["BIOS发布日期"] = dmtfDate(bios.ReleaseDate, false)
		if bios.SMBIOSMajorVersion > 0 {
			result["SMBIOS版本"] = fmt.Sprintf("%d.%d", bios.SMBIOSMajorVersion, bios.SMBIOSMinorVersion)
		}
		result["系统UUID"] = textOr(bios.SerialNumber, "未知")
	}
	return result
}

func queryPowerInfo() interface{} {
	var rows []batteryWMI
	if err := queryHardware("SELECT Name, Manufacturer, DesignCapacity, FullChargeCapacity, BatteryStatus, EstimatedRunTime FROM Win32_Battery", &rows); err != nil || len(rows) == 0 {
		return nil
	}
	row := rows[0]
	status := map[uint16]string{1: "放电中", 2: "充电中", 3: "充满电", 4: "低电量", 5: "关键电量"}[row.BatteryStatus]
	if status == "" {
		status = fmt.Sprintf("未知状态: %d", row.BatteryStatus)
	}
	remaining := "无法估算"
	if row.EstimatedRunTime != 0xFFFFFFFF {
		remaining = fmt.Sprintf("%d 分钟", row.EstimatedRunTime)
	}
	return map[string]interface{}{"电源名称": textOr(row.Name, "未知型号"), "制造商": textOr(row.Manufacturer, "未知制造商"), "设计容量_mWh": row.DesignCapacity, "当前容量_mWh": row.FullChargeCapacity, "电源状态": status, "估计剩余时间_分钟": remaining}
}

// CollectHardwareInfo collects hardware directly through WMI, without starting
// PowerShell. Individual WMI classes are isolated so unsupported classes on a
// particular Win7 image only produce null fields.
func CollectHardwareInfo() (string, error) {
	info := hardwareInfo{CollectionTime: time.Now().Format("2006-01-02 15:04:05"), System: querySystemInfo(), Computer: queryComputerInfo(), Processor: queryProcessorInfo(), Memory: queryMemoryInfo(), Disks: queryDiskInfo(), RAID: queryRAIDInfo(), GPU: queryGPUInfo(), Audio: queryAudioInfo(), NIC: queryNICInfo(), Network: queryNetworkInfo(), Motherboard: queryMotherboardInfo(), Power: queryPowerInfo()}
	data, err := json.Marshal(info)
	if err != nil {
		return "", fmt.Errorf("serialize hardware info: %w", err)
	}
	return string(data), nil
}
