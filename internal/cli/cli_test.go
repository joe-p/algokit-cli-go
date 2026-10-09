package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/joe-p/algokit-cli-go/internal/clierr"
)

func TestChunksBreakOnHyphens(t *testing.T) {
	got := chunks("see https://dev.algorand.co/getting-started/algokit-quick-start/. re-a x--y")
	want := []string{"see", " ", "https://dev.algorand.co/getting-", "started/algokit-", "quick-", "start/.", " ", "re-a", " ", "x", "--", "y"}
	if !slices.Equal(got, want) {
		t.Errorf("chunks = %q\nwant %q", got, want)
	}
}

func TestWrapLikeTextwrap(t *testing.T) {
	got := wrap("Number of lines to show from the end of the logs for each container.  [default: all]", 30, "", "")
	want := []string{"Number of lines to show from", "the end of the logs for each", "container.  [default: all]"}
	if !slices.Equal(got, want) {
		t.Errorf("wrap = %q\nwant %q", got, want)
	}
}

func testCommand(run func(ctx *Context) error) *Command {
	return &Command{
		Name: "root",
		Options: []*Option{
			{Key: "verbose", Opts: []string{"-v", "--verbose"}, Kind: Flag},
		},
		Commands: []*Command{{
			Name: "cmd",
			Options: []*Option{
				{Key: "name", Opts: []string{"--name", "-n"}, Kind: Value},
				{Key: "dev", Opts: []string{"--dev", "-d"}, SecondaryOpts: []string{"--no-dev"}, Kind: BoolPair, Default: true},
				{Key: "force", Opts: []string{"-f"}, Kind: Flag},
			},
			Arguments: []*Argument{{Key: "rest", Metavar: "REST", Variadic: true}},
			Run:       run,
		}},
	}
}

func TestParseOptions(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		name  string
		dev   bool
		force bool
		rest  []string
	}{
		{args: []string{"cmd"}, dev: true},
		{args: []string{"cmd", "--name", "x", "--no-dev"}, name: "x"},
		{args: []string{"cmd", "--name=x=y", "-d"}, name: "x=y", dev: true},
		{args: []string{"cmd", "-nfoo"}, name: "foo", dev: true},
		{args: []string{"cmd", "-fnfoo"}, name: "foo", dev: true, force: true},
		{args: []string{"cmd", "a", "-f", "b"}, dev: true, force: true, rest: []string{"a", "b"}},
		{args: []string{"cmd", "--", "-f", "--name"}, dev: true, rest: []string{"-f", "--name"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var ctx *Context
			err := Execute(testCommand(func(c *Context) error { ctx = c; return nil }), tc.args)
			if err != nil {
				t.Fatal(err)
			}
			name, _ := ctx.String("name")
			if name != tc.name || ctx.Bool("dev") != tc.dev || ctx.Bool("force") != tc.force || !slices.Equal(ctx.Args("rest"), tc.rest) {
				t.Errorf("name=%q dev=%v force=%v rest=%q", name, ctx.Bool("dev"), ctx.Bool("force"), ctx.Args("rest"))
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		message string
		usage   string
	}{
		{[]string{"cmd", "--bogus"}, "No such option: --bogus", "Usage: root cmd [OPTIONS] [REST]..."},
		{[]string{"cmd", "-x"}, "No such option: -x", "Usage: root cmd [OPTIONS] [REST]..."},
		{[]string{"cmd", "--name"}, "Option '--name' requires an argument.", ""},
		{[]string{"cmd", "--dev=1"}, "Option '--dev' does not take a value.", ""},
		{[]string{"cmd", "-f", "-v"}, "No such option: -v", "Usage: root cmd [OPTIONS] [REST]..."},
		{[]string{"-v"}, "Missing command.", "Usage: root [OPTIONS] COMMAND [ARGS]..."},
		{[]string{"other"}, "No such command 'other'.", "Usage: root [OPTIONS] COMMAND [ARGS]..."},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			err := Execute(testCommand(func(*Context) error { return nil }), tc.args)
			var usage *clierr.UsageError
			if !errors.As(err, &usage) || usage.Message != tc.message || usage.Usage != tc.usage {
				t.Errorf("err = %#v", err)
			}
		})
	}
}
