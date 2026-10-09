// Package cli is a small command line framework that reproduces the parsing rules, help layout and error messages
// of Python's click, so that this CLI behaves the same as the Python AlgoKit CLI.
package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/joe-p/algokit-cli-go/internal/clierr"
	"github.com/joe-p/algokit-cli-go/internal/log"
)

// OptionKind is the type of an Option.
type OptionKind int

const (
	// Flag is a boolean option that is set when present (e.g. --force).
	Flag OptionKind = iota
	// BoolPair is an on/off option pair (e.g. --dev/--no-dev).
	BoolPair
	// Value is an option that takes a value (e.g. --name NAME).
	Value
)

// Option is a command option.
type Option struct {
	// Key identifies the option's value in the Context.
	Key string
	// Opts are the option's names, e.g. []string{"-n", "--name"}.
	Opts []string
	// SecondaryOpts are the names that turn a BoolPair option off, e.g. []string{"--no-dev"}.
	SecondaryOpts []string
	Kind          OptionKind
	// Metavar is shown in help for Value options (default "TEXT").
	Metavar string
	Help    string
	// Default is the default value (a string for Value options, a bool for others; nil for no default).
	Default any
	// DefaultFunc, if set, computes the default value; ok is false if there is no default.
	DefaultFunc func() (value any, ok bool)
	ShowDefault bool
	Hidden      bool
	// Convert validates and converts a Value option's value; its error is reported as an invalid value.
	Convert func(value string) (string, error)
	// Eager options (--help, --version) are actioned as soon as parsing succeeds, before anything else.
	Eager func(ctx *Context) error
}

func (o *Option) takesValue() bool { return o.Kind == Value }

// Argument is a positional argument.
type Argument struct {
	Key string
	// Metavar is the name used in usage and error messages, e.g. "GOAL_ARGS" or "[docker|podman]".
	Metavar  string
	Choices  []string
	Required bool
	// Variadic arguments consume all remaining positional arguments (nargs=-1).
	Variadic bool
}

func (a *Argument) usage() string {
	metavar := a.Metavar
	if !a.Required {
		metavar = "[" + metavar + "]"
	}
	if a.Variadic {
		metavar += "..."
	}
	return metavar
}

// Command is a command, or a group of subcommands.
type Command struct {
	Name string
	// ShortHelp is shown in the parent group's list of commands.
	ShortHelp string
	// Help is shown at the top of the command's own help (paragraphs separated by blank lines).
	Help      string
	Options   []*Option
	Arguments []*Argument
	Commands  []*Command
	// IgnoreUnknownOptions passes unrecognised options through as positional arguments.
	IgnoreUnknownOptions bool
	// OnParsed is invoked as soon as this command's own options have been parsed (like click option callbacks).
	OnParsed func(ctx *Context) error
	// Before is invoked for a group before its subcommand runs, with the name of the subcommand.
	Before func(ctx *Context, subcommand string) error
	// Run is invoked for a (non-group) command.
	Run func(ctx *Context) error
}

func (c *Command) isGroup() bool { return len(c.Commands) > 0 }

func (c *Command) command(name string) *Command {
	for _, sub := range c.Commands {
		if sub.Name == name {
			return sub
		}
	}
	return nil
}

// Context holds the parsed values for an invocation of a command.
type Context struct {
	Command *Command
	Parent  *Context
	values  map[string]any
	args    map[string][]string
	eager   []*Option
}

// Path returns the command path, e.g. "algokit localnet start".
func (ctx *Context) Path() string {
	if ctx.Parent == nil {
		return ctx.Command.Name
	}
	return ctx.Parent.Path() + " " + ctx.Command.Name
}

// Bool returns the value of a boolean option.
func (ctx *Context) Bool(key string) bool {
	value, _ := ctx.values[key].(bool)
	return value
}

// String returns the value of a Value option (or a single argument), and whether it has a value.
func (ctx *Context) String(key string) (string, bool) {
	value, ok := ctx.values[key].(string)
	return value, ok
}

// Value returns an option or argument's value, and whether it has one (given, or from a default).
func (ctx *Context) Value(key string) (any, bool) {
	value, ok := ctx.values[key]
	return value, ok
}

// Args returns the values of a variadic argument.
func (ctx *Context) Args(key string) []string { return ctx.args[key] }

// Usage returns the command's usage line, e.g. "Usage: algokit localnet start [OPTIONS]".
func (ctx *Context) Usage() string {
	pieces := []string{"[OPTIONS]"}
	if ctx.Command.isGroup() {
		pieces = append(pieces, "COMMAND [ARGS]...")
	}
	for _, arg := range ctx.Command.Arguments {
		pieces = append(pieces, arg.usage())
	}
	return "Usage: " + ctx.Path() + " " + strings.Join(pieces, " ")
}

func (ctx *Context) usageError(format string, args ...any) *clierr.UsageError {
	return &clierr.UsageError{
		Message:  fmt.Sprintf(format, args...),
		Usage:    ctx.Usage(),
		HelpHint: ctx.Path() + " -h",
	}
}

// HelpOption returns the standard -h/--help option.
func HelpOption() *Option {
	return &Option{
		Key:  "help",
		Opts: []string{"-h", "--help"},
		Kind: Flag,
		Help: "Show this message and exit.",
		Eager: func(ctx *Context) error {
			log.Echo(ctx.Help())
			return &clierr.Exit{Code: 0}
		},
	}
}

func (c *Command) allOptions() []*Option {
	return append(slices.Clone(c.Options), HelpOption())
}

// parser is a port of click's OptionParser.
type parser struct {
	ctx     *Context
	options []*Option
	largs   []string
	rargs   []string
	// interspersed allows options after positional arguments (false for groups, so parsing stops at the subcommand)
	interspersed bool
}

func (p *parser) find(name string) (*Option, bool) {
	for _, option := range p.options {
		if slices.Contains(option.Opts, name) {
			return option, true
		}
		if slices.Contains(option.SecondaryOpts, name) {
			return option, false
		}
	}
	return nil, false
}

func isShortOpt(name string) bool {
	return len(name) == 2 && name[0] == '-' && name[1] != '-'
}

func (p *parser) parse(args []string) error {
	p.rargs = slices.Clone(args)
	for len(p.rargs) > 0 {
		arg := p.rargs[0]
		p.rargs = p.rargs[1:]
		switch {
		case arg == "--":
			return nil
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			if err := p.processOpts(arg); err != nil {
				return err
			}
		case p.interspersed:
			p.largs = append(p.largs, arg)
		default:
			p.rargs = append([]string{arg}, p.rargs...)
			return nil
		}
	}
	return nil
}

func (p *parser) processOpts(arg string) error {
	longOpt, explicitValue, hasValue := strings.Cut(arg, "=")
	option, primary := p.find(longOpt)
	if option != nil && !isShortOpt(longOpt) {
		if option.takesValue() {
			if hasValue {
				p.rargs = append([]string{explicitValue}, p.rargs...)
			}
			return p.takeValue(longOpt, option)
		}
		if hasValue {
			return &clierr.UsageError{Message: fmt.Sprintf("Option '%s' does not take a value.", longOpt)}
		}
		p.store(option, primary, "")
		return nil
	}
	if !strings.HasPrefix(arg, "--") {
		return p.matchShortOpt(arg)
	}
	if p.ctx.Command.IgnoreUnknownOptions {
		p.largs = append(p.largs, arg)
		return nil
	}
	return p.ctx.usageError("No such option: %s", longOpt)
}

func (p *parser) matchShortOpt(arg string) error {
	var unknown []rune
	chars := []rune(arg[1:])
	for i, ch := range chars {
		name := "-" + string(ch)
		option, primary := p.find(name)
		if option == nil {
			if p.ctx.Command.IgnoreUnknownOptions {
				unknown = append(unknown, ch)
				continue
			}
			return p.ctx.usageError("No such option: %s", name)
		}
		if option.takesValue() {
			// any characters left in arg are the value
			if rest := string(chars[i+1:]); rest != "" {
				p.rargs = append([]string{rest}, p.rargs...)
			}
			if err := p.takeValue(name, option); err != nil {
				return err
			}
			break
		}
		p.store(option, primary, "")
	}
	if len(unknown) > 0 {
		p.largs = append(p.largs, "-"+string(unknown))
	}
	return nil
}

func (p *parser) takeValue(name string, option *Option) error {
	if len(p.rargs) == 0 {
		return &clierr.UsageError{Message: fmt.Sprintf("Option '%s' requires an argument.", name)}
	}
	value := p.rargs[0]
	p.rargs = p.rargs[1:]
	p.store(option, true, value)
	return nil
}

func (p *parser) store(option *Option, primary bool, value string) {
	if option.Eager != nil && !slices.Contains(p.ctx.eager, option) {
		p.ctx.eager = append(p.ctx.eager, option)
	}
	if option.takesValue() {
		p.ctx.values[option.Key] = value
	} else {
		p.ctx.values[option.Key] = primary
	}
}

// parseArgs parses this command's own options and arguments, returning any remaining arguments for a subcommand.
func (ctx *Context) parseArgs(args []string) ([]string, error) {
	cmd := ctx.Command
	ctx.values = map[string]any{}
	ctx.args = map[string][]string{}
	p := &parser{ctx: ctx, options: cmd.allOptions(), interspersed: !cmd.isGroup()}
	if err := p.parse(args); err != nil {
		return nil, err
	}
	for _, option := range ctx.eager {
		if err := option.Eager(ctx); err != nil {
			return nil, err
		}
	}

	for _, option := range cmd.Options {
		value, given := ctx.values[option.Key]
		if !given {
			if option.DefaultFunc != nil {
				value, given = option.DefaultFunc()
			} else if option.Default != nil {
				value, given = option.Default, true
			}
			if !given {
				continue
			}
			ctx.values[option.Key] = value
		}
		if str, ok := value.(string); ok && option.Convert != nil {
			converted, err := option.Convert(str)
			if err != nil {
				hints := make([]string, len(option.Opts))
				for i, opt := range option.Opts {
					hints[i] = "'" + opt + "'"
				}
				return nil, ctx.usageError("Invalid value for %s: %v", strings.Join(hints, " / "), err)
			}
			ctx.values[option.Key] = converted
		}
	}

	positional := append(p.largs, p.rargs...)
	if cmd.isGroup() {
		return positional, nil
	}
	for _, arg := range cmd.Arguments {
		if arg.Variadic {
			ctx.args[arg.Key] = positional
			positional = nil
			continue
		}
		if len(positional) == 0 {
			if arg.Required {
				return nil, ctx.usageError("Missing argument '%s'.", arg.Metavar)
			}
			continue
		}
		value := positional[0]
		positional = positional[1:]
		if len(arg.Choices) > 0 && !slices.Contains(arg.Choices, value) {
			quoted := make([]string, len(arg.Choices))
			for i, choice := range arg.Choices {
				quoted[i] = "'" + choice + "'"
			}
			return nil, ctx.usageError("Invalid value for '%s': '%s' is not one of %s.",
				arg.usage(), value, strings.Join(quoted, ", "))
		}
		ctx.values[arg.Key] = value
	}
	if len(positional) > 0 {
		plural := ""
		if len(positional) > 1 {
			plural = "s"
		}
		return nil, ctx.usageError("Got unexpected extra argument%s (%s)", plural, strings.Join(positional, " "))
	}
	return nil, nil
}

// Execute parses args for the root command and runs the selected command.
//
// Unlike click, a subcommand's arguments (including --help) are parsed before its parent group's Before callback
// runs, so `algokit localnet start --help` doesn't need a container engine.
func Execute(root *Command, args []string) error {
	ctx := &Context{Command: root}
	var chain []*Context
	for {
		if ctx.Command.isGroup() && len(args) == 0 {
			// like click's no_args_is_help
			log.Echo(ctx.Help())
			return nil
		}
		rest, err := ctx.parseArgs(args)
		if err != nil {
			return err
		}
		if ctx.Command.OnParsed != nil {
			if err := ctx.Command.OnParsed(ctx); err != nil {
				return err
			}
		}
		chain = append(chain, ctx)
		if !ctx.Command.isGroup() {
			break
		}
		if len(rest) == 0 {
			return ctx.usageError("Missing command.")
		}
		sub := ctx.Command.command(rest[0])
		if sub == nil {
			return ctx.usageError("No such command '%s'.", rest[0])
		}
		ctx, args = &Context{Command: sub, Parent: ctx}, rest[1:]
	}

	for i, c := range chain[:len(chain)-1] {
		if c.Command.Before != nil {
			if err := c.Command.Before(c, chain[i+1].Command.Name); err != nil {
				return err
			}
		}
	}
	return ctx.Command.Run(ctx)
}
