package robustio

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// renameRetries 为替换失败后的最多重试次数，第 n 次重试前等待 5 × 2^(n-1) ms
const renameRetries = 5

// Rename 以 os.Rename 替换文件，源或目标被杀毒软件、索引器暂时占用（ERROR_ACCESS_DENIED、ERROR_SHARING_VIOLATION）时重试
func Rename(from, to string) error {
	delay := 5 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err := os.Rename(from, to)
		if err == nil || attempt == renameRetries || !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
}
