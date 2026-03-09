package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("gogoclaw %s\n", version)
		return
	}

	fmt.Println("GoGoClaw — Security-first AI agent framework")
	fmt.Printf("Version: %s\n", version)
}
