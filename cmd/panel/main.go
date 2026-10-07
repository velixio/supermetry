package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: panel {serve|migrate|close-days|version}")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "version":
		fmt.Printf("panel %s\n", version)
	case "serve", "migrate", "close-days":
		fmt.Fprintln(os.Stderr, "not implemented")
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "usage: panel {serve|migrate|close-days|version}")
		os.Exit(2)
	}
}
