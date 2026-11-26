package main

import (
	"fmt"
	"os"

	"github.com/linsu/flywaygo/core"
)

// 用法：go run ./verify/cmd/checksum <file>...
// 输出格式与 FlywayChecksum.java 保持一致：<path>\t<checksum>
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: checksum <file>...")
		os.Exit(1)
	}
	for _, path := range os.Args[1:] {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 %s 失败: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("%s\t%d\n", path, core.Checksum(string(raw)))
	}
}
