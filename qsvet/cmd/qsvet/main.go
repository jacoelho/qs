// Command qsvet runs the qs analyzers. Use it as a vet tool:
//
//	go install github.com/jacoelho/qs/qsvet/cmd/qsvet@latest
//	go vet -vettool="$(command -v qsvet)" ./...
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/jacoelho/qs/qsvet/discarded"
)

func main() { singlechecker.Main(discarded.Analyzer) }
