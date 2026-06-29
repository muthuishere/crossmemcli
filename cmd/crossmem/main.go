package main

import (
	"fmt"
	"os"

	"github.com/muthuishere/crossmemcli/internal/app"
	"github.com/muthuishere/crossmemcli/internal/diag"
)

func main() {
	diag.Debugf("argv=%q", os.Args[1:])
	if err := app.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		diag.Debugf("exit=1 err=%q", err)
		os.Exit(1)
	}
	diag.Debugf("exit=0")
}
