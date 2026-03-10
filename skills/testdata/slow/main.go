// Slow skill: sleeps forever to test timeout enforcement.
// Compile: GOOS=wasip1 GOARCH=wasm go build -o slow.wasm .
package main

import "time"

func main() {
	time.Sleep(24 * time.Hour)
}
