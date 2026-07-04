package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/saker-ai/gonacos/internal/contract"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "gonacos-contract: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("gonacos-contract", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", ".", "gonacos project root")
	manifest := fs.String("manifest", filepath.Join("api", "openapi", "manifest.json"), "manifest path relative to root")
	write := fs.Bool("write", false, "write generated manifest")
	verify := fs.Bool("verify", false, "verify generated manifest matches the checked-in manifest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *write == *verify {
		return fmt.Errorf("choose exactly one of -write or -verify")
	}

	manifestPath := *manifest
	if !filepath.IsAbs(manifestPath) {
		manifestPath = filepath.Join(*root, manifestPath)
	}

	if *verify {
		return contract.Verify(*root, manifestPath)
	}

	current, err := contract.Build(*root)
	if err != nil {
		return err
	}
	return contract.Write(current, manifestPath)
}
