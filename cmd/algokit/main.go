// Command algokit is a Go port of the AlgoKit CLI's localnet and goal commands.
package main

import (
	"os"

	"github.com/joe-p/algokit-cli-go/internal/commands"
)

func main() {
	os.Exit(commands.Main(os.Args[1:]))
}
