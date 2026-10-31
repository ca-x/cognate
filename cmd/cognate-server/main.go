package main

import (
	"os"

	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/servercli"
)

func main() {
	if handled, code := state.SnapshotWorkerDispatch(os.Args); handled {
		os.Exit(code)
	}
	servercli.Execute()
}
