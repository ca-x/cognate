package main

import (
	"os"

	"github.com/ca-x/cognate/internal/cli"
	"github.com/ca-x/cognate/internal/secretservice"
	"github.com/ca-x/cognate/internal/server/state"
)

func main() {
	if handled, code := state.SnapshotWorkerDispatch(os.Args); handled {
		os.Exit(code)
	}
	if code, handled := secretservice.HelperDispatch(); handled {
		os.Exit(code)
	}
	cli.Execute()
}
