package win32

// 默认音频设备主音量的读取与设置。
//
// 通过 mmdeviceapi（COM）激活默认渲染端点的 IAudioEndpointVolume，
// 直接设置主音量标量（0.0-1.0），与 SendInput 音量键的步进方式互补：
// 步进方式无法可靠地"设置到指定百分比"，关机前降音量需要精确设置。
//
// float 参数 ABI 说明（Go 1.26 / windows/amd64 实测验证）：
// COM 方法 SetMasterVolumeLevelScalar(fLevel float32, pguidEventContext)
// 的 float 参数按 x64 调用约定应放入 XMM 寄存器；Go 的 syscall.SyscallN
// 只接收整数，但会同步把整数寄存器的值装载进对应的 XMM 寄存器，
// 因此以 uintptr(math.Float32bits(v))（float32 位型塞入整数寄存器）传参
// 可以正确落地。实测 0.00/0.05/0.20/0.37/0.55/0.80/1.00 全部精确生效；
// 而 float64 位型（Float64bits）会被 COM 按 float32 重读低 32 位而失效。
// 纯整数寄存器（XMM 未装载）环境下此调用约定是否成立未验证，
// windows/amd64 下 syscall.SyscallN 始终同步装载 XMM，与实际运行环境一致。

import (
	"fmt"
	"math"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	ole32                = syscall.NewLazyDLL("ole32.dll")
	procCoInitialize     = ole32.NewProc("CoInitialize")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
)

// COM 标识符（字符串形式，运行时解析为 syscall.GUID）。
const (
	clsidMMDeviceEnumerator = "{BCDE0395-E52F-467C-8E3D-C4579291692E}"
	iidIMMDeviceEnumerator  = "{A95664D2-9614-4F35-A746-DE8DB63617E6}"
	iidIAudioEndpointVolume = "{5CDF2C82-841E-4546-9722-0CF74078229A}"
)

// COM HRESULT。
const (
	comSOK         = 0x00000000
	comSFalse      = 0x00000001
	comChangedMode = 0x80010106 // RPC_E_CHANGED_MODE：线程已按其他套间模型初始化
	clsctxAll      = 1          // CLSCTX_ALL
	rolemultimedia = 1          // eMultimedia（GetDefaultAudioEndpoint 的 role 参数）
)

// parseGUID 解析 "{XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX}" 形式的 GUID 字符串。
func parseGUID(str string) (g syscall.GUID) {
	hex2byte := func(c byte) byte {
		switch {
		case c >= '0' && c <= '9':
			return c - '0'
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10
		}
		return 0
	}
	readHex := func(n int, off *int) uint64 {
		var v uint64
		for i := 0; i < n; i++ {
			v = v<<4 | uint64(hex2byte(str[*off]))
			*off++
		}
		return v
	}
	p := 1
	g.Data1 = uint32(readHex(8, &p))
	p++ // '-'
	g.Data2 = uint16(readHex(4, &p))
	p++
	g.Data3 = uint16(readHex(4, &p))
	p++
	for i := 0; i < 8; i++ {
		if str[p] == '-' {
			p++
		}
		g.Data4[i] = byte(readHex(2, &p))
	}
	return g
}

// vtblSlot 读取 COM 对象 vtable 中第 idx 个方法指针。
func vtblSlot(vtbl unsafe.Pointer, idx int) uintptr {
	return *(*uintptr)(unsafe.Add(vtbl, uintptr(idx)*uintptr(ptrSize)))
}

// releaseCOM 调用 IUnknown::Release 释放 COM 接口。
func releaseCOM(obj unsafe.Pointer) {
	if obj == nil {
		return
	}
	vtbl := *(*unsafe.Pointer)(obj)
	syscall.SyscallN(vtblSlot(vtbl, 2), uintptr(obj))
}

// IMMDeviceEnumerator vtable：[0..2] IUnknown，[3]EnumAudioEndpoints，
// [4]GetDefaultAudioEndpoint；IMMDevice：[3]Activate；
// IAudioEndpointVolume：[7]SetMasterVolumeLevelScalar，[9]GetMasterVolumeLevelScalar。
const (
	slotGetDefaultAudioEndpoint    = 4
	slotIMMDeviceActivate          = 3
	slotSetMasterVolumeLevelScalar = 7
	slotGetMasterVolumeLevelScalar = 9
)

// withAudioEndpointVolume 在锁定线程上初始化 COM，激活当前默认渲染设备的
// IAudioEndpointVolume 并执行 fn，结束后释放全部 COM 资源。
//
// COM 初始化具有线程亲和性，必须在同一 OS 线程内完成整个会话：
// 用 runtime.LockOSThread 把当前 goroutine 固定到线程后再初始化。
func withAudioEndpointVolume(fn func(epv, vtbl unsafe.Pointer) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitialize.Call(0)
	switch sc := uint32(hr); sc {
	case comSOK, comSFalse:
		defer procCoUninitialize.Call()
	case comChangedMode:
		// 线程已按其他套间模型初始化：沿用现有模式，不再配对 CoUninitialize。
	default:
		return fmt.Errorf("CoInitialize 失败: 0x%08x", sc)
	}

	clsid := parseGUID(clsidMMDeviceEnumerator)
	iidEnum := parseGUID(iidIMMDeviceEnumerator)
	var enum unsafe.Pointer
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsid)),
		0, // pUnkOuter
		clsctxAll,
		uintptr(unsafe.Pointer(&iidEnum)),
		uintptr(unsafe.Pointer(&enum)),
	)
	if uint32(hr) != comSOK {
		return fmt.Errorf("创建 MMDeviceEnumerator 失败: 0x%08x", uint32(hr))
	}
	defer releaseCOM(enum)

	// enum->GetDefaultAudioEndpoint(eRender, eMultimedia, &dev)
	vtblEnum := *(*unsafe.Pointer)(enum)
	var dev unsafe.Pointer
	hr, _, _ = syscall.SyscallN(vtblSlot(vtblEnum, slotGetDefaultAudioEndpoint),
		uintptr(enum),
		0, // eRender
		rolemultimedia,
		uintptr(unsafe.Pointer(&dev)),
	)
	if uint32(hr) != comSOK {
		return fmt.Errorf("获取默认音频设备失败: 0x%08x", uint32(hr))
	}
	defer releaseCOM(dev)

	// dev->Activate(IID_IAudioEndpointVolume, CLSCTX_ALL, nil, &epv)
	// 每次调用都重新激活：默认设备可能切换（如插拔耳机），不缓存接口指针。
	vtblDev := *(*unsafe.Pointer)(dev)
	iidVol := parseGUID(iidIAudioEndpointVolume)
	var epv unsafe.Pointer
	hr, _, _ = syscall.SyscallN(vtblSlot(vtblDev, slotIMMDeviceActivate),
		uintptr(dev),
		uintptr(unsafe.Pointer(&iidVol)),
		clsctxAll,
		0,
		uintptr(unsafe.Pointer(&epv)),
	)
	if uint32(hr) != comSOK {
		return fmt.Errorf("激活音量接口失败: 0x%08x", uint32(hr))
	}
	defer releaseCOM(epv)

	return fn(epv, *(*unsafe.Pointer)(epv))
}

// volumePercentToLevel 把 0-100 的百分比换算为 COM 的 0.0-1.0 标量。
// 独立成纯函数便于单元测试换算与钳位逻辑。
func volumePercentToLevel(percent float64) float32 {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return float32(percent / 100)
}

// volumeLevelToPercent 把 0.0-1.0 标量换算为 0-100 百分比。
func volumeLevelToPercent(level float32) float64 {
	return float64(level) * 100
}

// GetMasterVolumePercent 返回当前默认音频设备的主音量百分比（0-100）。
func GetMasterVolumePercent() (float64, error) {
	var percent float64
	err := withAudioEndpointVolume(func(epv, vtbl unsafe.Pointer) error {
		var level float32
		hr, _, _ := syscall.SyscallN(vtblSlot(vtbl, slotGetMasterVolumeLevelScalar),
			uintptr(epv),
			uintptr(unsafe.Pointer(&level)),
		)
		if uint32(hr) != comSOK {
			return fmt.Errorf("GetMasterVolumeLevelScalar 失败: 0x%08x", uint32(hr))
		}
		percent = volumeLevelToPercent(level)
		return nil
	})
	return percent, err
}

// SetMasterVolumePercent 设置当前默认音频设备的主音量百分比（0-100，超出范围自动钳位）。
func SetMasterVolumePercent(percent float64) error {
	level := volumePercentToLevel(percent)
	return withAudioEndpointVolume(func(epv, vtbl unsafe.Pointer) error {
		hr, _, _ := syscall.SyscallN(vtblSlot(vtbl, slotSetMasterVolumeLevelScalar),
			uintptr(epv),
			uintptr(math.Float32bits(level)), // float32 位型：见文件头 ABI 说明
			0,                                // pguidEventContext
		)
		if uint32(hr) != comSOK {
			return fmt.Errorf("SetMasterVolumeLevelScalar 失败: 0x%08x", uint32(hr))
		}
		return nil
	})
}
