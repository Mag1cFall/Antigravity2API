//go:build !desktop

package main

import "context"

// run 执行子命令，没有子命令时等同于 serve，不带任何参数时启动后打开管理端
func run(ctx context.Context, args []string) error {
	if done, err := dispatch(ctx, args); done {
		return err
	}
	return serveCommand(ctx, args, len(args) == 0)
}
