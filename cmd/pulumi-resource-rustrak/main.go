package main

import (
	"context"
	"fmt"
	"os"

	"github.com/bambamboole/pulumi-provider-rustrak/provider"
)

// Version of the provider, replaced at release build time.
var version = "0.1.0"

func main() {
	p, err := provider.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s", err.Error())
		os.Exit(1)
	}

	if err := p.Run(context.Background(), "rustrak", version); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s", err.Error())
		os.Exit(1)
	}
}
