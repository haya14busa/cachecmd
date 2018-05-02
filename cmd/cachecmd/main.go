package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const version = "0.9.0"

const usageMessage = "" +
	`Usage:	cachecmd [flags]
`

func usage() {
	fmt.Fprintln(os.Stderr, usageMessage)
	fmt.Fprintln(os.Stderr, "Flags:")
	flag.PrintDefaults()
	os.Exit(2)
}

type option struct {
	version bool
}

var flagOpt = &option{}

func init() {
	flag.BoolVar(&flagOpt.version, "version", false, "print version")
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if flagOpt.version {
		fmt.Fprintln(os.Stderr, version)
		return
	}
	if err := run(os.Stdin, os.Stdout, os.Stderr, flagOpt, flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "cachecmd: %v\n", err)
		os.Exit(1)
	}
}

func run(r io.Reader, w io.Writer, stderr io.Writer, opt *option, args []string) error {
	return nil
}
