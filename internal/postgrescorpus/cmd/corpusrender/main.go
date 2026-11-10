// Command corpusrender answers the offline corpus tooling: it reads
// {"id","tree"} JSON lines on stdin and writes one
// {"id","outcome","detail","sql","args"} line per tree on stdout.
//
// The output is a candidate only. The caller reparses the SQL with an
// independent PostgreSQL parser before anything is frozen as verified.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jacoelho/qs/internal/postgrescorpus"
)

type request struct {
	ID   *int            `json:"id"`
	Tree json.RawMessage `json:"tree"`
}

type response struct {
	postgrescorpus.Rendering
	ID int `json:"id"`
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "corpusrender:", err)
		os.Exit(1)
	}
}

func run(input io.Reader, output io.Writer) error {
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	writer := bufio.NewWriter(output)
	encoder := json.NewEncoder(writer)
	for line := 1; ; line++ {
		var item request
		err := decoder.Decode(&item)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("request %d: %w", line, err)
		}
		if item.ID == nil || len(item.Tree) == 0 {
			return fmt.Errorf("request %d: missing id or tree", line)
		}
		rendering, err := postgrescorpus.Render(*item.ID, item.Tree)
		if err != nil {
			return fmt.Errorf("request %d: %w", line, err)
		}
		if rendering.Args == nil {
			rendering.Args = []string{}
		}
		if err := encoder.Encode(response{Rendering: rendering, ID: *item.ID}); err != nil {
			return fmt.Errorf("request %d id=%d: %w", line, *item.ID, err)
		}
	}
	return writer.Flush()
}
