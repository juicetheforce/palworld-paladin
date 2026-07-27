// Command paladin is the entrypoint for Paladin, a self-hosted admin panel
// for a single Palworld dedicated server. This file does wiring only; all
// application logic lives under internal/ (see docs/DESIGN.md §5.4).
package main

import "fmt"

var version = "0.0.0-dev"

func main() {
	fmt.Printf("paladin %s — pre-alpha scaffold; see docs/DESIGN.md\n", version)
}
