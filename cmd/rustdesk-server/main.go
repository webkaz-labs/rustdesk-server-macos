// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"fmt"
	"os"

	"github.com/webkaz-labs/rustdesk-server-macos/internal/service"
)

var version = "dev"

func main() {
	if err := service.Run(os.Args[1:], os.Stdin, os.Stdout, version); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
