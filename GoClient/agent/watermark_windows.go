//go:build windows

package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 窗口样式与扩展样式。
const (
	wsPopup = 0x80000000

	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000

	hwndTopmost   = ^uintptr(0) // HWND_TOPMOST = -1
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010
	swpShowWindow = 0x0040

	swShow = 5

	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmTimer         = 0x0113
	wmDisplayChange = 0x007E
	wmEndSession    = 0x0016

	timerReassertTopmost = 1

	// gmAdvanced 是使用 SetWorldTransform 的前提。
	gmAdvanced = 2

	dibRgbColors       = 0
	antialiasedQuality = 4
	defaultCharSet     = 1
	defaultPitchFamily = 0

	transparentBkMode = 1

	// ulwAlpha 表示按每像素 alpha 合成，配合 32 位 DIB 才能得到平滑的水印边缘。
	ulwAlpha   = 0x02
	acSrcOver  = 0x00
	acSrcAlpha = 0x01

	monitorInfoFlagsPrimary = 0x01

	// 虚拟屏幕的 GetSystemMetrics 索引。枚举显示器失败时用它兜底，
	// 保证多屏环境下水印不会整体消失。
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	// watermarkWindowClass 是水印窗口类名，带项目前缀避免与用户会话里的其他窗口冲突。
	watermarkWindowClass = "JikeAgentWatermarkOverlay"
	// watermarkMutexName 是全局命名互斥体，服务据此判断是否已有水印进程在运行。
	// 进程被结束后内核会自动释放它，因此它比 PID 文件更可靠。
	watermarkMutexName = "Global\\JikeAgentWatermarkOverlay"

	watermarkFontFace = "Microsoft YaHei"

	// mutexSynchronizeAccess 是打开互斥体所需的最小权限。
	mutexSynchronizeAccess = 0x00100000
	// errorAlreadyExistsErrno 表示互斥体已存在，即已有水印进程在运行。
	errorAlreadyExistsErrno = syscall.Errno(183)

	fwNormalWeight    = 400
	outDefaultPrecis  = 0
	clipDefaultPrecis = 0
)

var (
	modUser32Watermark = syscall.NewLazyDLL("user32.dll")
	modGdi32Watermark  = syscall.NewLazyDLL("gdi32.dll")
	modKernel32Mutex   = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW    = modUser32Watermark.NewProc("RegisterClassExW")
	procCreateWindowExW     = modUser32Watermark.NewProc("CreateWindowExW")
	procDefWindowProcW      = modUser32Watermark.NewProc("DefWindowProcW")
	procGetMessageW         = modUser32Watermark.NewProc("GetMessageW")
	procTranslateMessage    = modUser32Watermark.NewProc("TranslateMessage")
	procDispatchMessageW    = modUser32Watermark.NewProc("DispatchMessageW")
	procPostQuitMessage     = modUser32Watermark.NewProc("PostQuitMessage")
	procDestroyWindow       = modUser32Watermark.NewProc("DestroyWindow")
	procSetTimer            = modUser32Watermark.NewProc("SetTimer")
	procSetWindowPos        = modUser32Watermark.NewProc("SetWindowPos")
	procShowWindow          = modUser32Watermark.NewProc("ShowWindow")
	procEnumDisplayMonitors = modUser32Watermark.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = modUser32Watermark.NewProc("GetMonitorInfoW")
	procUpdateLayeredWindow = modUser32Watermark.NewProc("UpdateLayeredWindow")
	procGetDC               = modUser32Watermark.NewProc("GetDC")
	procReleaseDC           = modUser32Watermark.NewProc("ReleaseDC")
	procSetProcessDPIAware  = modUser32Watermark.NewProc("SetProcessDPIAware")
	procGetSystemMetrics    = modUser32Watermark.NewProc("GetSystemMetrics")

	procCreateCompatibleDC    = modGdi32Watermark.NewProc("CreateCompatibleDC")
	procDeleteDC              = modGdi32Watermark.NewProc("DeleteDC")
	procCreateDIBSection      = modGdi32Watermark.NewProc("CreateDIBSection")
	procDeleteObject          = modGdi32Watermark.NewProc("DeleteObject")
	procSelectObject          = modGdi32Watermark.NewProc("SelectObject")
	procCreateFontW           = modGdi32Watermark.NewProc("CreateFontW")
	procSetTextColor          = modGdi32Watermark.NewProc("SetTextColor")
	procSetBkMode             = modGdi32Watermark.NewProc("SetBkMode")
	procSetGraphicsMode       = modGdi32Watermark.NewProc("SetGraphicsMode")
	procSetWorldTransform     = modGdi32Watermark.NewProc("SetWorldTransform")
	procTextOutW              = modGdi32Watermark.NewProc("TextOutW")
	procGetTextExtentPoint32W = modGdi32Watermark.NewProc("GetTextExtentPoint32W")

	procCreateMutexW     = modKernel32Mutex.NewProc("CreateMutexW")
	procOpenMutexW       = modKernel32Mutex.NewProc("OpenMutexW")
	procGetModuleHandleW = modKernel32Mutex.NewProc("GetModuleHandleW")
)

// Win32 结构体。这里自行声明而不是依赖 x/sys，避免版本差异导致字段布局不一致。
type wmRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func (r wmRect) width() int  { return int(r.Right - r.Left) }
func (r wmRect) height() int { return int(r.Bottom - r.Top) }

type wmPoint struct {
	X int32
	Y int32
}

type wmSize struct {
	CX int32
	CY int32
}

type wmMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      wmPoint
}

type wmClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type wmMonitorInfo struct {
	CbSize   uint32
	Monitor  wmRect
	WorkArea wmRect
	Flags    uint32
}

type wmXForm struct {
	EM11 float32
	EM12 float32
	EM21 float32
	EM22 float32
	EDx  float32
	EDy  float32
}

type wmBitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type wmBitmapInfo struct {
	Header wmBitmapInfoHeader
	Colors [1]uint32
}

type wmBlendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

// watermarkRenderOptions 是水印渲染参数，由服务端下发并持久化在终端本地。
type watermarkRenderOptions struct {
	Text        string
	Opacity     int
	Color       string
	FontSize    int
	Angle       int
	SpacingX    int
	SpacingY    int
	SessionID   uint32
	PidFilePath string
	ResultPath  string
}

// watermarkWindow 记录一个显示器对应的叠层窗口。
type watermarkWindow struct {
	hwnd uintptr
	area wmRect
}

// watermarkRenderer 是子进程内的全局渲染状态。
type watermarkRenderer struct {
	options       watermarkRenderOptions
	red           byte
	green         byte
	blue          byte
	windows       []watermarkWindow
	mutex         windows.Handle
	paintedMinute string
}

var activeRenderer = &watermarkRenderer{}

// RunWatermarkChild 是 --watermark 子命令入口。它由服务进程以 SYSTEM 令牌投递到
// 登录用户的交互式桌面，因此窗口对用户可见，而普通用户无法结束该进程。
func RunWatermarkChild(args []string) int {
	// 窗口消息队列与 GDI 对象都是线程亲和的，必须把当前 goroutine 锁定到固定 OS 线程，
	// 否则 Go 调度器把 goroutine 迁移到别的线程后窗口行为会异常。
	runtime.LockOSThread()

	values := parseChildFlags(args)
	activeRenderer.options = watermarkRenderOptions{
		Text:        values["text"],
		Opacity:     flagInt(values, "opacity", defaultWatermarkOpacity),
		Color:       values["color"],
		FontSize:    flagInt(values, "fontSize", defaultWatermarkFontSize),
		Angle:       flagInt(values, "angle", defaultWatermarkAngle),
		SpacingX:    flagInt(values, "spacingX", 0),
		SpacingY:    flagInt(values, "spacingY", 0),
		SessionID:   uint32(flagInt(values, "session", 0)),
		PidFilePath: values["pidfile"],
		ResultPath:  values[childResultFlag],
	}
	if strings.TrimSpace(activeRenderer.options.Color) == "" {
		activeRenderer.options.Color = defaultWatermarkColor
	}
	if activeRenderer.options.Opacity < 0 {
		activeRenderer.options.Opacity = 0
	}
	if activeRenderer.options.Opacity > 100 {
		activeRenderer.options.Opacity = 100
	}
	activeRenderer.red, activeRenderer.green, activeRenderer.blue = parseHexColor(activeRenderer.options.Color)

	if strings.TrimSpace(activeRenderer.options.Text) == "" {
		writeResultFile(activeRenderer.options.ResultPath, marshalWatermarkResult(map[string]interface{}{
			"ok": false, "problem": "水印文案为空",
		}))
		return 1
	}

	mutex, alreadyExists, err := acquireWatermarkMutex()
	if err != nil {
		writeResultFile(activeRenderer.options.ResultPath, marshalWatermarkResult(map[string]interface{}{
			"ok": false, "problem": err.Error(),
		}))
		return 1
	}
	if alreadyExists {
		// 已有一个水印进程在运行，直接退出，避免叠出两层水印。
		windows.CloseHandle(mutex)
		writeResultFile(activeRenderer.options.ResultPath, marshalWatermarkResult(map[string]interface{}{
			"ok": true, "skipped": true, "reason": "已有水印进程在运行",
		}))
		return 0
	}
	activeRenderer.mutex = mutex

	// 先做 DPI 感知再创建窗口，否则高分屏上位置与字号都会偏。
	procSetProcessDPIAware.Call()

	if err := activeRenderer.writePidFile(); err != nil {
		activeRenderer.cleanup()
		writeResultFile(activeRenderer.options.ResultPath, marshalWatermarkResult(map[string]interface{}{
			"ok": false, "problem": "写入水印PID文件失败: " + err.Error(),
		}))
		return 1
	}

	if err := activeRenderer.rebuild(); err != nil {
		activeRenderer.cleanup()
		writeResultFile(activeRenderer.options.ResultPath, marshalWatermarkResult(map[string]interface{}{
			"ok": false, "problem": err.Error(),
		}))
		return 1
	}

	writeResultFile(activeRenderer.options.ResultPath, marshalWatermarkResult(map[string]interface{}{
		"ok":       true,
		"pid":      os.Getpid(),
		"session":  activeRenderer.options.SessionID,
		"monitors": len(activeRenderer.windows),
		"text":     activeRenderer.currentText(),
	}))

	procSetTimer.Call(activeRenderer.windows[0].hwnd, timerReassertTopmost, 1000, 0)
	activeRenderer.runMessageLoop()
	activeRenderer.cleanup()
	return 0
}

// runMessageLoop 是水印进程的主循环，直到收到退出消息。
func (r *watermarkRenderer) runMessageLoop() {
	var message wmMsg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		result := int32(ret)
		if result == 0 || result == -1 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

// watermarkWndProc 是所有显示器水印窗口共用的窗口过程。
func watermarkWndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch uint32(message) {
	case wmTimer:
		// 其他置顶窗口会抢走 z-order，必须周期性重新压顶。
		activeRenderer.reassertTopmost()
		activeRenderer.repaintIfMinuteChanged()
		return 0
	case wmDisplayChange:
		// 分辨率或显示器数量变化后重建窗口，否则水印会残留在旧位置。
		_ = activeRenderer.rebuild()
		return 0
	case wmEndSession, wmClose, wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return ret
}

// reassertTopmost 把所有水印窗口重新压到最顶层。
func (r *watermarkRenderer) reassertTopmost() {
	for _, window := range r.windows {
		procSetWindowPos.Call(window.hwnd, hwndTopmost, 0, 0, 0, 0,
			uintptr(swpNoMove|swpNoSize|swpNoActivate))
	}
}

// repaintIfMinuteChanged 在分钟变化时重绘，用于刷新 {DateTime} 占位符。
func (r *watermarkRenderer) repaintIfMinuteChanged() {
	current := time.Now().Format("2006-01-02 15:04")
	if current == r.paintedMinute {
		return
	}
	for _, window := range r.windows {
		_ = r.paintWindow(window.hwnd, window.area)
	}
}

// rebuild 销毁旧窗口并按当前显示器布局重建。
func (r *watermarkRenderer) rebuild() error {
	for _, window := range r.windows {
		procDestroyWindow.Call(window.hwnd)
	}
	r.windows = nil

	if err := r.registerWindowClass(); err != nil {
		return err
	}
	areas := enumerateMonitorRects()
	if len(areas) == 0 {
		return fmt.Errorf("未能枚举到任何显示器")
	}

	className, err := windows.UTF16PtrFromString(watermarkWindowClass)
	if err != nil {
		return err
	}
	emptyName, err := windows.UTF16PtrFromString("")
	if err != nil {
		return err
	}
	instance := currentModuleHandle()

	for _, area := range areas {
		hwnd, _, _ := procCreateWindowExW.Call(
			uintptr(wsExLayered|wsExTransparent|wsExTopmost|wsExToolWindow|wsExNoActivate),
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(emptyName)),
			uintptr(wsPopup),
			uintptr(uint32(area.Left)), uintptr(uint32(area.Top)),
			uintptr(uint32(area.width())), uintptr(uint32(area.height())),
			0, 0, uintptr(instance), 0)
		if hwnd == 0 {
			return fmt.Errorf("创建水印窗口失败: %v", windows.GetLastError())
		}
		if err := r.paintWindow(hwnd, area); err != nil {
			procDestroyWindow.Call(hwnd)
			return err
		}
		procShowWindow.Call(hwnd, swShow)
		r.windows = append(r.windows, watermarkWindow{hwnd: hwnd, area: area})
	}

	r.paintedMinute = time.Now().Format("2006-01-02 15:04")
	r.reassertTopmost()
	return nil
}

// registerWindowClass 注册水印窗口类，重复注册时忽略"类已存在"错误。
func (r *watermarkRenderer) registerWindowClass() error {
	className, err := windows.UTF16PtrFromString(watermarkWindowClass)
	if err != nil {
		return err
	}
	windowClass := wmClassEx{
		Style:         0,
		LpfnWndProc:   syscall.NewCallback(watermarkWndProc),
		HInstance:     uintptr(currentModuleHandle()),
		LpszClassName: className,
	}
	windowClass.CbSize = uint32(unsafe.Sizeof(windowClass))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&windowClass)))
	return nil
}

// currentModuleHandle 返回本模块句柄，注册窗口类与创建窗口都需要它。
func currentModuleHandle() windows.Handle {
	handle, _, _ := procGetModuleHandleW.Call(0)
	return windows.Handle(handle)
}

// enumerateMonitorRects 枚举所有显示器的设备坐标矩形。
// 矩形一律通过 GetMonitorInfoW 读取：EnumDisplayMonitors 回调里的 lprc 是一个裸地址，
// 把 uintptr 转回 unsafe.Pointer 属于 go vet 拒绝的危险转换，而 GetMonitorInfoW 本来就够用。
// 万一一个显示器都没枚举到，就退回整个虚拟屏幕范围，避免水印整体消失。
func enumerateMonitorRects() []wmRect {
	var areas []wmRect
	callback := syscall.NewCallback(func(hMonitor, hdc, lprc, lParam uintptr) uintptr {
		info := wmMonitorInfo{CbSize: uint32(unsafe.Sizeof(wmMonitorInfo{}))}
		if ret, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&info))); ret != 0 {
			areas = append(areas, info.Monitor)
		}
		return 1
	})
	procEnumDisplayMonitors.Call(0, 0, callback, 0)
	if len(areas) > 0 {
		return areas
	}
	return virtualScreenRect()
}

// virtualScreenRect 返回覆盖全部显示器的虚拟屏幕矩形；尺寸取不到时返回 nil。
func virtualScreenRect() []wmRect {
	width := systemMetric(smCXVirtualScreen)
	height := systemMetric(smCYVirtualScreen)
	if width <= 0 || height <= 0 {
		return nil
	}
	left := systemMetric(smXVirtualScreen)
	top := systemMetric(smYVirtualScreen)
	return []wmRect{{
		Left:   left,
		Top:    top,
		Right:  left + width,
		Bottom: top + height,
	}}
}

// systemMetric 读取 GetSystemMetrics 的有符号返回值，多屏排列时坐标可能为负。
func systemMetric(index int32) int32 {
	value, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int32(value)
}

// currentText 返回替换终端侧占位符后的水印文案。
// {UserName} 在每次绘制时解析，换人登录后水印会自动跟着变。
func (r *watermarkRenderer) currentText() string {
	return substituteWatermarkPlaceholders(r.options.Text, currentAccountName(), currentSessionID(), time.Now())
}

// acquireWatermarkMutex 创建全局命名互斥体。服务端也用它判断是否已有水印进程存活：
// 进程被强制结束后内核会自动释放互斥体，因此它比 PID 文件更可靠。
func acquireWatermarkMutex() (windows.Handle, bool, error) {
	name, err := windows.UTF16PtrFromString(watermarkMutexName)
	if err != nil {
		return 0, false, err
	}
	handle, _, _ := procCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return 0, false, fmt.Errorf("创建水印互斥体失败: %v", windows.GetLastError())
	}
	alreadyExists := false
	if errno, ok := syscall.GetLastError().(syscall.Errno); ok && errno == errorAlreadyExistsErrno {
		alreadyExists = true
	}
	return windows.Handle(handle), alreadyExists, nil
}

// openWatermarkMutex 尝试打开全局互斥体，用于判断水印进程是否存活。
func openWatermarkMutex() windows.Handle {
	name, err := windows.UTF16PtrFromString(watermarkMutexName)
	if err != nil {
		return 0
	}
	handle, _, _ := procOpenMutexW.Call(uintptr(mutexSynchronizeAccess), 0, uintptr(unsafe.Pointer(name)))
	return windows.Handle(handle)
}

// writePidFile 记录自身 PID 与会话号，服务重启后据此接管或清理已有水印进程。
func (r *watermarkRenderer) writePidFile() error {
	if strings.TrimSpace(r.options.PidFilePath) == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]interface{}{
		"pid":       os.Getpid(),
		"sessionId": r.options.SessionID,
		"startedAt": time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return writeFileAtomic(r.options.PidFilePath, payload)
}

// cleanup 在退出前销毁窗口、删除 PID 文件并释放互斥体。
func (r *watermarkRenderer) cleanup() {
	for _, window := range r.windows {
		procDestroyWindow.Call(window.hwnd)
	}
	r.windows = nil
	if strings.TrimSpace(r.options.PidFilePath) != "" {
		_ = os.Remove(r.options.PidFilePath)
	}
	if r.mutex != 0 {
		windows.CloseHandle(r.mutex)
		r.mutex = 0
	}
}

// paintWindow 把一个显示器的水印画进 32 位 DIB，再用 UpdateLayeredWindow 合成到桌面。
func (r *watermarkRenderer) paintWindow(hwnd uintptr, area wmRect) error {
	width := area.width()
	height := area.height()
	if width <= 0 || height <= 0 {
		return nil
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return fmt.Errorf("获取屏幕DC失败")
	}
	defer procReleaseDC.Call(0, screenDC)

	memoryDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memoryDC == 0 {
		return fmt.Errorf("创建内存DC失败")
	}
	defer procDeleteDC.Call(memoryDC)

	info := wmBitmapInfo{}
	info.Header.Size = uint32(unsafe.Sizeof(wmBitmapInfoHeader{}))
	info.Header.Width = int32(width)
	// 负高度表示自顶向下的位图，行序与内存布局一致，便于逐像素处理。
	info.Header.Height = -int32(height)
	info.Header.Planes = 1
	info.Header.BitCount = 32
	info.Header.Compression = dibRgbColors

	var bits unsafe.Pointer
	bitmap, _, _ := procCreateDIBSection.Call(memoryDC, uintptr(unsafe.Pointer(&info)),
		dibRgbColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return fmt.Errorf("创建DIB失败: %v", windows.GetLastError())
	}
	defer procDeleteObject.Call(bitmap)

	oldBitmap, _, _ := procSelectObject.Call(memoryDC, bitmap)
	defer procSelectObject.Call(memoryDC, oldBitmap)

	// 关键手法：先在黑底上用白色绘制抗锯齿文字，灰度值即为每个像素的覆盖率，
	// 再把覆盖率换算成 alpha，就能得到边缘平滑的半透明水印，而不需要 GDI+。
	clearPixelsToBlack(bits, width, height)
	drawTiledText(memoryDC, width, height, r.currentText(), r.options)
	applyPixelAlpha(bits, width, height, r.red, r.green, r.blue, r.options.Opacity)

	position := wmPoint{X: area.Left, Y: area.Top}
	windowSize := wmSize{CX: int32(width), CY: int32(height)}
	sourcePoint := wmPoint{X: 0, Y: 0}
	blend := wmBlendFunction{
		BlendOp:             acSrcOver,
		BlendFlags:          0,
		SourceConstantAlpha: 255,
		AlphaFormat:         acSrcAlpha,
	}

	ret, _, _ := procUpdateLayeredWindow.Call(hwnd, screenDC,
		uintptr(unsafe.Pointer(&position)), uintptr(unsafe.Pointer(&windowSize)),
		memoryDC, uintptr(unsafe.Pointer(&sourcePoint)), 0,
		uintptr(unsafe.Pointer(&blend)), ulwAlpha)
	if ret == 0 {
		return fmt.Errorf("UpdateLayeredWindow失败: %v", windows.GetLastError())
	}
	return nil
}

// clearPixelsToBlack 把整块 DIB 清零。
func clearPixelsToBlack(bits unsafe.Pointer, width, height int) {
	pixels := unsafe.Slice((*byte)(bits), width*height*4)
	for i := range pixels {
		pixels[i] = 0
	}
}

// drawTiledText 以指定倾角平铺绘制水印文字。
func drawTiledText(hdc uintptr, width, height int, text string, options watermarkRenderOptions) {
	if strings.TrimSpace(text) == "" {
		return
	}

	fontSize := options.FontSize
	if fontSize <= 0 {
		fontSize = defaultWatermarkFontSize
	}
	faceName, err := windows.UTF16PtrFromString(watermarkFontFace)
	if err != nil {
		return
	}
	// 负的字体高度表示以像素为单位指定字符高度。
	font, _, _ := procCreateFontW.Call(
		uintptr(uint32(int32(-fontSize))), 0, 0, 0,
		uintptr(fwNormalWeight), 0, 0, 0,
		defaultCharSet, outDefaultPrecis, clipDefaultPrecis,
		antialiasedQuality, defaultPitchFamily,
		uintptr(unsafe.Pointer(faceName)))
	if font == 0 {
		return
	}
	defer procDeleteObject.Call(font)

	oldFont, _, _ := procSelectObject.Call(hdc, font)
	defer procSelectObject.Call(hdc, oldFont)

	procSetBkMode.Call(hdc, transparentBkMode)
	// 白色文字（COLORREF 为 0x00BBGGRR），后续按覆盖率换成真正的水印颜色。
	procSetTextColor.Call(hdc, uintptr(0x00FFFFFF))

	encoded, err := windows.UTF16FromString(text)
	if err != nil || len(encoded) == 0 {
		return
	}
	textPointer := uintptr(unsafe.Pointer(&encoded[0]))
	textLength := uintptr(len(encoded) - 1)

	var extent wmSize
	procGetTextExtentPoint32W.Call(hdc, textPointer, textLength, uintptr(unsafe.Pointer(&extent)))

	spacingX := options.SpacingX
	if spacingX <= 0 {
		spacingX = int(extent.CX) + fontSize*6
	}
	spacingY := options.SpacingY
	if spacingY <= 0 {
		spacingY = int(extent.CY) + fontSize*5
	}
	if spacingX < 8 {
		spacingX = 8
	}
	if spacingY < 8 {
		spacingY = 8
	}

	// 旋转整个逻辑坐标系，之后在逻辑坐标里平铺即可得到斜向排列的水印。
	procSetGraphicsMode.Call(hdc, gmAdvanced)
	radians := float64(options.Angle) * math.Pi / 180
	cosine := math.Cos(radians)
	sine := math.Sin(radians)
	centerX := float32(width) / 2
	centerY := float32(height) / 2
	transform := wmXForm{
		EM11: float32(cosine),
		EM12: float32(sine),
		EM21: float32(-sine),
		EM22: float32(cosine),
		// 平移量使逻辑坐标的中心点映射到设备坐标的中心点，实现绕中心旋转。
		EDx: centerX - centerX*float32(cosine) + centerY*float32(sine),
		EDy: centerY - centerX*float32(sine) - centerY*float32(cosine),
	}
	procSetWorldTransform.Call(hdc, uintptr(unsafe.Pointer(&transform)))

	// 旋转后需要用对角线长度作为平铺边界，否则四角会露出空白。
	halfDiagonal := int(math.Sqrt(float64(width*width+height*height)))/2 + spacingX + spacingY
	baseX := int32(centerX)
	baseY := int32(centerY)
	for offsetY := -halfDiagonal; offsetY <= halfDiagonal; offsetY += spacingY {
		for offsetX := -halfDiagonal; offsetX <= halfDiagonal; offsetX += spacingX {
			procTextOutW.Call(hdc,
				uintptr(uint32(baseX+int32(offsetX))),
				uintptr(uint32(baseY+int32(offsetY))),
				textPointer, textLength)
		}
	}
}

// applyPixelAlpha 把灰度覆盖率换算成每像素 alpha，并替换成配置的水印颜色。
// DIB 的内存顺序是 BGRA。
func applyPixelAlpha(bits unsafe.Pointer, width, height int, red, green, blue byte, opacity int) {
	if width <= 0 || height <= 0 {
		return
	}
	if opacity < 0 {
		opacity = 0
	}
	if opacity > 100 {
		opacity = 100
	}
	pixels := unsafe.Slice((*byte)(bits), width*height*4)
	for i := 0; i+3 < len(pixels); i += 4 {
		coverage := pixels[i]
		if pixels[i+1] > coverage {
			coverage = pixels[i+1]
		}
		if pixels[i+2] > coverage {
			coverage = pixels[i+2]
		}
		if coverage == 0 {
			pixels[i] = 0
			pixels[i+1] = 0
			pixels[i+2] = 0
			pixels[i+3] = 0
			continue
		}
		pixels[i] = blue
		pixels[i+1] = green
		pixels[i+2] = red
		pixels[i+3] = byte(int(coverage) * opacity / 100)
	}
}
