// Text skill: provides text manipulation tools.
// Compile: GOOS=wasip1 GOARCH=wasm go build -o text.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type envelope struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

type textArgs struct {
	Text string `json:"text"`
}

type output struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError("read stdin: " + err.Error())
		return
	}

	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		writeError("parse envelope: " + err.Error())
		return
	}

	var args textArgs
	if err := json.Unmarshal(env.Args, &args); err != nil {
		writeError("parse args: " + err.Error())
		return
	}

	var result string
	switch env.Tool {
	case "text_uppercase":
		result = strings.ToUpper(args.Text)
	case "text_lowercase":
		result = strings.ToLower(args.Text)
	case "text_wordcount":
		words := strings.Fields(args.Text)
		result = fmt.Sprintf("%d", len(words))
	case "text_reverse":
		runes := []rune(args.Text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		result = string(runes)
	default:
		writeError("unknown tool: " + env.Tool)
		return
	}

	writeResult(result)
}

func writeResult(r string) {
	out := output{Result: r}
	enc, _ := json.Marshal(out)
	os.Stdout.Write(enc)
}

func writeError(e string) {
	out := output{Error: e}
	enc, _ := json.Marshal(out)
	os.Stderr.Write(enc)
	os.Exit(1)
}
