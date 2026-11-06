// Command soap shows what's running on a Mac: listening ports, connections and memory.
package main

import (
	"fmt"
	"os"

	"github.com/KhomerikiK/soap/internal/cli"
)

func main() {
	d, err := cli.RealDeps()
	if err != nil {
		fmt.Fprintln(os.Stderr, "soap:", err)
		os.Exit(1)
	}
	os.Exit(cli.Run(d, os.Args[1:]))
}
