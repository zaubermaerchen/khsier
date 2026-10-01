// Package main is the thin khsier executable adapter.
package main

import (
	"os"

	"github.com/zaubermaerchen/khsier/internal/khsier"
)

func main() {
	os.Exit(khsier.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
