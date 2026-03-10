// Echo skill: reads JSON from stdin, echoes it back with a prefix.
// Compile: GOOS=wasip1 GOARCH=wasm go build -o echo.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type input struct {
	Message string `json:"message"`
}

type output struct {
	Result string `json:"result"`
}

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, `{"error":"read stdin: %s"}`, err)
		os.Exit(1)
	}

	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintf(os.Stderr, `{"error":"parse json: %s"}`, err)
		os.Exit(1)
	}

	out := output{Result: "echo: " + in.Message}
	enc, _ := json.Marshal(out)
	os.Stdout.Write(enc)
}
