package main

import (
	"fmt"
	"os"

	"github.com/jmcampanini/gibson/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "gibson: error: %v\n", err)
		os.Exit(1)
	}
}
