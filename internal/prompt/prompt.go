// Package prompt asks the user questions on the terminal, equivalent to click.confirm and questionary.select.
package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/joe-p/algokit-cli-go/internal/clierr"
	"github.com/joe-p/algokit-cli-go/internal/log"
)

// Input is where answers are read from; replaceable for testing.
var Input io.Reader = os.Stdin

var (
	reader       *bufio.Reader
	readerSource io.Reader
)

func readLine() (string, error) {
	if reader == nil || readerSource != Input {
		reader, readerSource = bufio.NewReader(Input), Input
	}
	line, err := reader.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// Confirm prompts for a yes/no answer (equivalent to click.confirm); returns an Abort error on EOF.
// Replaceable for testing.
var Confirm = func(message string, defaultValue bool) (bool, error) {
	suffix := "[y/N]"
	if defaultValue {
		suffix = "[Y/n]"
	}
	for {
		log.WriteStdout(message + " " + suffix + ": ")
		answer, err := readLine()
		if err != nil {
			return false, &clierr.Abort{}
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return defaultValue, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		log.WriteStdout("Error: invalid input\n")
	}
}

// Select prompts the user to choose one of the given choices, returning the chosen index, or -1 if none was chosen
// (e.g. on EOF). Replaceable for testing.
var Select = func(message string, choices []string) int {
	log.WriteStdout("? " + message + "\n")
	for i, choice := range choices {
		log.WriteStdout(fmt.Sprintf("  %d) %s\n", i+1, choice))
	}
	for {
		log.WriteStdout(fmt.Sprintf("Answer [1-%d]: ", len(choices)))
		answer, err := readLine()
		if err != nil {
			log.WriteStdout("\n")
			return -1
		}
		answer = strings.TrimSpace(answer)
		if n, convErr := strconv.Atoi(answer); convErr == nil && n >= 1 && n <= len(choices) {
			return n - 1
		}
		for i, choice := range choices {
			if strings.EqualFold(strings.Fields(choice)[0], answer) {
				return i
			}
		}
		log.WriteStdout("Error: invalid input\n")
	}
}
