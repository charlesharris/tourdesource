// Command tds (tour-de-source) analyzes a source repository and produces a
// shareable, interactive tour of the project. See docs/design.md.
package main

import "github.com/charlesharris/tourdesource/internal/cli"

func main() {
	cli.Execute()
}
