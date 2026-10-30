package main

import (
	"os"

	"github.com/ca-x/cognate/internal/cli"
	"github.com/ca-x/cognate/internal/secretservice"
)

func main() {
	if code, handled := secretservice.HelperDispatch(); handled {
		os.Exit(code)
	}
	cli.Execute()
}
