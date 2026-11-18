package app

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// knownFolders 是常用位置对应的 Windows 已知文件夹
var knownFolders = map[string]*windows.KNOWNFOLDERID{
	"downloads": windows.FOLDERID_Downloads,
	"desktop":   windows.FOLDERID_Desktop,
	"documents": windows.FOLDERID_Documents,
}

// userDir 返回 Windows 已知文件夹的当前位置，用户移动过的文件夹取移动后的位置
func userDir(_ string, name string) string {
	dir, _ := windows.KnownFolderPath(knownFolders[name], 0)
	return dir
}

// reveal 打开资源管理器并选中 path
func reveal(path string) error {
	command := exec.Command("explorer.exe")
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	if err := command.Start(); err != nil {
		return err
	}
	go command.Wait()
	return nil
}
