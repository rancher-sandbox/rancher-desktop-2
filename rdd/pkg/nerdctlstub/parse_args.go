// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package nerdctlstub rewrites nerdctl command lines so that arguments
// referring to host paths work when nerdctl runs inside the guest VM. The
// parser is ported from Rancher Desktop 1.x (src/go/nerdctl-stub); the
// command table is generated from the pinned nerdctl module by ./generate.
package nerdctlstub

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
)

type cleanupFunc func() error

// ParsedArgs is the result of translating a nerdctl command line.
type ParsedArgs struct {
	// Args are the arguments with host paths replaced.
	Args []string
	// cleanup functions to call after nerdctl exits.
	cleanup []cleanupFunc
}

// RunCleanups releases any resources created during translation. Call it
// after nerdctl exits, whether or not the command succeeded.
func (p *ParsedArgs) RunCleanups() error {
	return runCleanups(p.cleanup)
}

func runCleanups(cleanups []cleanupFunc) error {
	var errs []error
	for _, cleanup := range cleanups {
		errs = append(errs, cleanup())
	}
	return errors.Join(errs...)
}

// argHandler is the type of a function that rewrites one argument value.
type argHandler func(string) (string, []cleanupFunc, error)

// argHandlersType bundles the rewrite functions passed to command handlers.
type argHandlersType struct {
	volumeArgHandler       argHandler
	filePathArgHandler     argHandler
	outputPathArgHandler   argHandler
	mountArgHandler        argHandler
	builderCacheArgHandler argHandler
	buildContextArgHandler argHandler
}

// commandHandlerType is the type of commandDefinition.handler, which is used
// to handle positional arguments. The passed-in arguments exclude any flags
// given after positional arguments.
type commandHandlerType func(*commandDefinition, []string, argHandlersType) (*ParsedArgs, error)

type commandDefinition struct {
	// commands points to the command map to use for lookups; if this is
	// nil, the package-level commands map is used. Tests set their own.
	commands *map[string]commandDefinition
	// aliases maps alias paths to canonical paths in the command map; if
	// this is nil, the package-level commandAliases map is used.
	aliases *map[string]string
	// commandPath is the arguments needed to reach this command.
	commandPath string
	// subcommands that can be spawned from this command.
	subcommands map[string]struct{}
	// options for this (sub)command. If the handler is nil, the option
	// does not take an argument.
	options map[string]argHandler
	// hasForeignFlags means option parsing stops at the first positional
	// argument, because later flags belong to the command run inside the
	// container (e.g. `nerdctl run image ls --color`).
	hasForeignFlags bool
	// handler for any positional arguments. If this is nil, subcommands
	// are looked up and other positional arguments pass through.
	handler commandHandlerType
}

// lookup finds a command definition by path, resolving aliases.
func (c *commandDefinition) lookup(path string) (commandDefinition, bool) {
	commandMap := c.commands
	if commandMap == nil {
		commandMap = &commands
	}
	if def, ok := (*commandMap)[path]; ok {
		return def, true
	}
	aliasMap := c.aliases
	if aliasMap == nil {
		aliasMap = &commandAliases
	}
	if canonical, ok := (*aliasMap)[path]; ok {
		if def, ok := (*commandMap)[canonical]; ok {
			return def, true
		}
	}
	return commandDefinition{}, false
}

// parseOption takes an argument (that is known to start with `-` or `--`)
// plus the next argument (which may be needed if a value is required), and
// returns the rewritten arguments, whether the value argument was consumed,
// plus any cleanup functions.
func (c *commandDefinition) parseOption(arg, next string) (newArgs []string, consumedNext bool, cleanups []cleanupFunc, err error) {
	if !strings.HasPrefix(arg, "-") {
		panic(fmt.Sprintf("commandDefinition.parseOption called with invalid arg %q", arg))
	}

	option := arg
	value := next
	consumed := true
	sep := strings.Index(option, "=")
	if sep >= 0 {
		value = option[sep+1:]
		option = option[:sep]
		consumed = false
	}
	handler, ok := c.options[option]
	if !ok {
		// There may be multiple single-character options bunched together,
		// e.g. `-itp 80`.
		if len(option) > 1 && option[0] == '-' && option[1] != '-' {
			// Make sure all options (except the last) exist and take no
			// arguments.
			for _, ch := range option[1 : len(option)-1] {
				handler, ok = c.options[fmt.Sprintf("-%c", ch)]
				if !ok || handler != nil {
					ok = false
					break
				}
			}
			// If all earlier options are fine, use the handler of the last.
			if ok {
				handler, ok = c.options[fmt.Sprintf("-%s", option[len(option)-1:])]
			}
		}
		if !ok {
			// The user may say `-foo` instead of `--foo`.
			option = "-" + option
			handler, ok = c.options[option]
		}
	}
	if ok {
		if handler == nil {
			// This does not consume a value, and needs no rewriting.
			return []string{arg}, false, nil, nil
		}
		converted, cleanups, err := handler(value)
		if err != nil {
			// Pass along any cleanups even on failure.
			return nil, consumed, cleanups, err
		}
		return []string{option, converted}, consumed, cleanups, nil
	}

	// Check if the parent command can resolve this option.
	var extraCleanups []cleanupFunc
	parentName := ""
	if lastSpace := strings.LastIndex(c.commandPath, " "); lastSpace > -1 {
		parentName = c.commandPath[:lastSpace]
	}
	if parentName != c.commandPath {
		parent, ok := c.lookup(parentName)
		if !ok {
			panic(fmt.Sprintf("command %q could not find parent %q", c.commandPath, parentName))
		}
		parentResult, parentConsumed, parentCleanups, parentErr := parent.parseOption(arg, next)
		if parentErr == nil {
			return parentResult, parentConsumed, parentCleanups, nil
		}
		extraCleanups = parentCleanups
	}
	return nil, false, extraCleanups, fmt.Errorf("command %q does not support option %s", c.commandPath, arg)
}

// parse arguments for this command; this includes options (--long, -x) as
// well as subcommands and positional arguments.
func (c commandDefinition) parse(args []string) (*ParsedArgs, error) {
	// Parsing rules:
	// - At each command level, short options (-x) at that level can be parsed.
	// - At each command level, long options from the current or any previous
	//   level can be parsed.
	// - Positional arguments can be intermixed with (long and short) options.
	// - If a command has foreign flags (e.g. `nerdctl run`), option parsing
	//   stops at the first positional argument. This means we parse the flag
	//   in `nerdctl run --env foo=bar image sh -c ...` but not the `--env`
	//   flag in `nerdctl run image --env foo=bar sh -c ...`.
	// - `--` stops parsing of options.
	var result ParsedArgs
	var positionalArgs []string
argLoop:
	for argIndex := 0; argIndex < len(args); argIndex++ {
		arg := args[argIndex]
		switch {
		case arg == "--":
			// No more options, only positional arguments.
			positionalArgs = append(positionalArgs, args[argIndex+1:]...)
			break argLoop
		case strings.HasPrefix(arg, "-"):
			next := ""
			if argIndex+1 < len(args) {
				next = args[argIndex+1]
			}
			newArgs, consumed, cleanups, err := c.parseOption(arg, next)
			if err != nil {
				// Run any cleanups we have so far.
				for _, cleanup := range append(cleanups, result.cleanup...) {
					if cleanupErr := cleanup(); cleanupErr != nil {
						log.Printf("Error running cleanup: %s", cleanupErr)
					}
				}
				return nil, err
			}
			result.Args = append(result.Args, newArgs...)
			result.cleanup = append(result.cleanup, cleanups...)
			if consumed {
				argIndex++
			}
		case len(c.subcommands) > 0:
			// This command has subcommands; assume any non-flag argument is
			// a subcommand and hand off parsing to it.
			subcommandPath := c.commandPath
			if subcommandPath != "" {
				subcommandPath += " "
			}
			subcommandPath += arg
			if subcommand, ok := c.lookup(subcommandPath); ok {
				childResult, err := subcommand.parse(args[argIndex+1:])
				if err != nil {
					return nil, err
				}
				result.Args = append(result.Args, arg)
				result.Args = append(result.Args, childResult.Args...)
				result.cleanup = append(result.cleanup, childResult.cleanup...)
			} else {
				// Unknown subcommand (e.g. `nerdctl internal`); pass all
				// remaining arguments through unmodified.
				result.Args = append(result.Args, args[argIndex:]...)
			}
			break argLoop
		case c.hasForeignFlags:
			// The rest of the arguments starting from the first positional
			// argument is foreign.
			positionalArgs = append(positionalArgs, args[argIndex:]...)
			break argLoop
		default:
			// This command has neither subcommands nor foreign arguments;
			// everything else is positional, but later flags still parse.
			positionalArgs = append(positionalArgs, arg)
		}
	}
	// At this point, `result` holds the options, and `positionalArgs` the
	// unparsed positional arguments.
	if c.handler != nil {
		childResult, err := c.handler(&c, positionalArgs, argHandlers)
		if err != nil {
			return nil, err
		}
		result.Args = append(result.Args, childResult.Args...)
		result.cleanup = append(result.cleanup, childResult.cleanup...)
	} else {
		if len(positionalArgs) > 0 && !slices.Contains(result.Args, "--") {
			result.Args = slices.Concat(result.Args, []string{"--"}, positionalArgs)
		} else {
			result.Args = append(result.Args, positionalArgs...)
		}
	}
	return &result, nil
}

// TranslateCommandLine parses a nerdctl command line (excluding the program
// name) and returns it with host paths replaced so that they work inside
// the guest VM.
func TranslateCommandLine(args []string) (*ParsedArgs, error) {
	root, ok := (&commandDefinition{}).lookup("")
	if !ok {
		panic("no root command definition")
	}
	return root.parse(args)
}

// ignoredArgHandler handles arguments that do not contain paths.
func ignoredArgHandler(input string) (string, []cleanupFunc, error) {
	return input, nil, nil
}

// kindHandlers maps the overlay classification to the rewrite functions.
func kindHandlers() map[argHandlerKind]argHandler {
	return map[argHandlerKind]argHandler{
		filePathArg:     argHandlers.filePathArgHandler,
		outputPathArg:   argHandlers.outputPathArgHandler,
		volumeArg:       argHandlers.volumeArgHandler,
		mountArg:        argHandlers.mountArgHandler,
		builderCacheArg: argHandlers.builderCacheArgHandler,
		buildContextArg: argHandlers.buildContextArgHandler,
	}
}

// commands is the runtime command table, built from the generated
// commandSpecs plus the pathFlags overlay and the positional handlers.
var commands = map[string]commandDefinition{}

func init() {
	for path, spec := range commandSpecs {
		def := commandDefinition{
			commandPath:     path,
			subcommands:     map[string]struct{}{},
			options:         map[string]argHandler{},
			hasForeignFlags: spec.foreignFlags,
		}
		for _, name := range spec.subcommands {
			def.subcommands[name] = struct{}{}
		}
		for _, name := range spec.valueFlags {
			def.options[name] = ignoredArgHandler
		}
		for _, name := range spec.boolFlags {
			def.options[name] = nil
		}
		commands[path] = def
	}

	// The pathFlags and notPathFlags classifications must stay disjoint.
	for path, flags := range notPathFlags {
		for _, name := range flags {
			if _, ok := pathFlags[path][name]; ok {
				panic(fmt.Sprintf("flag %q of command %q is in both pathFlags and notPathFlags", name, path))
			}
		}
	}

	// Apply the path-flag overlay. The generator validates these entries;
	// the panics only guard against the table and overlay going out of
	// sync without regeneration.
	handlers := kindHandlers()
	for path, flags := range pathFlags {
		def, ok := commands[path]
		if !ok {
			panic(fmt.Sprintf("pathFlags: unknown command %q", path))
		}
		for name, kind := range flags {
			if _, ok := def.options[name]; !ok {
				panic(fmt.Sprintf("pathFlags: command %q has no option %q", path, name))
			}
			def.options[name] = handlers[kind]
		}
	}

	// Positional-argument handlers. nerdctl v2.0.3 has no `image import`;
	// restore the 1.x imageImportHandler when a bump brings it back.
	for path, handler := range map[string]commandHandlerType{
		"builder build": builderBuildHandler,
		"container cp":  containerCopyHandler,
	} {
		def, ok := commands[path]
		if !ok {
			panic(fmt.Sprintf("command handler: unknown command %q", path))
		}
		def.handler = handler
		commands[path] = def
	}
}
