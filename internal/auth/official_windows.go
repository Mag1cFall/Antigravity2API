package auth

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// officialApp 返回官方客户端 antigravity:// 协议处理程序登记的可执行文件
func officialApp() (string, error) {
	key, err := registry.OpenKey(registry.CLASSES_ROOT, `antigravity\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("未找到官方客户端: %w", err)
	}
	defer key.Close()
	command, _, err := key.GetStringValue("")
	if err != nil {
		return "", fmt.Errorf("未找到官方客户端: %w", err)
	}
	if rest, ok := strings.CutPrefix(command, `"`); ok {
		app, _, _ := strings.Cut(rest, `"`)
		return app, nil
	}
	app, _, _ := strings.Cut(command, " ")
	return app, nil
}

// stopOfficial 结束可执行文件位于官方客户端安装目录内的全部进程并等待其退出，返回主进程的启动参数
func stopOfficial(app string) ([]string, error) {
	dir := strings.ToLower(filepath.Dir(app)) + `\`
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	var args []string
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for next := windows.Process32First(snapshot, &entry); next == nil; next = windows.Process32Next(snapshot, &entry) {
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, entry.ProcessID)
		if err != nil {
			continue
		}
		name := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(name))
		if windows.QueryFullProcessImageName(process, 0, &name[0], &size) == nil {
			if path := windows.UTF16ToString(name[:size]); strings.HasPrefix(strings.ToLower(path), dir) {
				if main, ok := mainArgs(process, path, app); ok {
					args = main
				}
				if err := windows.TerminateProcess(process, 1); err != nil && err != windows.ERROR_ACCESS_DENIED {
					windows.CloseHandle(process)
					return nil, fmt.Errorf("结束官方客户端进程 %d: %w", entry.ProcessID, err)
				}
				windows.WaitForSingleObject(process, windows.INFINITE)
			}
		}
		windows.CloseHandle(process)
	}
	return args, nil
}

// mainArgs 在 process 为官方客户端主进程（可执行文件为 app 且没有 --type= 参数）时返回其启动参数
func mainArgs(process windows.Handle, path, app string) ([]string, bool) {
	if !strings.EqualFold(path, app) {
		return nil, false
	}
	buffer := make([]byte, 4096)
	var size uint32
	err := windows.NtQueryInformationProcess(process, windows.ProcessCommandLineInformation, unsafe.Pointer(&buffer[0]), uint32(len(buffer)), &size)
	if err == windows.STATUS_INFO_LENGTH_MISMATCH {
		buffer = make([]byte, size)
		err = windows.NtQueryInformationProcess(process, windows.ProcessCommandLineInformation, unsafe.Pointer(&buffer[0]), uint32(len(buffer)), &size)
	}
	if err != nil {
		return nil, false
	}
	args, err := windows.DecomposeCommandLine((*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0])).String())
	if err != nil || len(args) == 0 {
		return nil, false
	}
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "--type=") {
			return nil, false
		}
	}
	return args[1:], true
}

// startOfficial 在安装目录中以 args 启动官方客户端
func startOfficial(app string, args []string) error {
	cmd := exec.Command(app, args...)
	cmd.Dir = filepath.Dir(app)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动官方客户端: %w", err)
	}
	return cmd.Process.Release()
}
