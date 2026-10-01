// release-tool is build/release tooling; it is not included in end-user archives.
package main

import (
	"fmt"
	"os"

	"github.com/webkaz-labs/rustdesk-server-macos/internal/release"
)

func main() {
	root, err := os.Getwd()
	if err == nil {
		var tool *release.Tool
		tool, err = release.New(root)
		if err == nil {
			err = tool.Run(os.Args[1:], os.Stdout)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release-tool:", err)
		os.Exit(1)
	}
}
