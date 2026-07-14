// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build linux

// Generate is stage 2 of the nerdctl parse-table generator. It rebuilds
// nerdctl's command tree from replica.go plus the constructors extracted by
// stage 1 (./extract), walks the tree, and writes the parse table to
// ../nerdctl_commands_generated.go. It also validates the hand-curated
// overlay in ../overlay.go: every entry must name an existing flag, and
// every value-consuming flag that smells like a path must be classified in
// the overlay or acknowledged in notPathFlags.
//
// This stage builds on Linux only (the //go:build tag above): the nerdctl
// packages of the pinned v2.0.3 do not compile elsewhere, and off-Linux
// builds would lack `container cp` and `apparmor` anyway. Use
// `make -C rdd generate-nerdctl`, which wraps the stages in a Linux
// container on other platforms; the CI regenerate-and-diff check on Linux
// is authoritative.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	nerdctlModule = "github.com/containerd/nerdctl/v2"
	defaultOutput = "../nerdctl_commands_generated.go"
	overlayFile   = "../overlay.go"
)

// commandSpec mirrors the fields emitted into the generated table; the
// authoritative type lives in the nerdctlstub package.
type commandSpec struct {
	valueFlags   []string
	boolFlags    []string
	subcommands  []string
	foreignFlags bool
	name         string            // cobra command name, for alias detection
	short        string            // cobra Short text, for alias detection
	flagUsage    map[string]string // per flag, for the path heuristic only
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("generate: ")
	output := flag.String("output", defaultOutput, "write the table to this file")
	flag.Parse()
	if err := run(*output); err != nil {
		log.Fatal(err)
	}
}

func run(output string) error {
	if !slices.Equal(slices.Sorted(slices.Values(unresolvedCommands)), slices.Sorted(slices.Values(replicaCommands))) {
		return fmt.Errorf("replica.go provides %v but extract found %v; update addReplicaCommands", replicaCommands, unresolvedCommands)
	}
	rootCmd := replicaRootCmd()
	if err := verifyRootFlags(rootCmd); err != nil {
		return err
	}
	specs := map[string]*commandSpec{}
	walk(rootCmd, "", specs)
	aliases := detectAliases(specs)
	overlay, err := loadOverlay(overlayFile)
	if err != nil {
		return err
	}
	if err := validateOverlay(specs, aliases, overlay); err != nil {
		return err
	}
	return emit(output, nerdctlPinnedVersion, specs, aliases)
}

// verifyRootFlags checks that replica.go registers exactly the flag names
// that stage 1 extracted from initRootCmdFlags.
func verifyRootFlags(rootCmd *cobra.Command) error {
	var got []string
	rootCmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		got = append(got, f.Name)
	})
	slices.Sort(got)
	want := slices.Sorted(slices.Values(expectedRootFlags))
	if !slices.Equal(got, want) {
		return fmt.Errorf("replica.go root flags do not match initRootCmdFlags:\n  replica: %v\n  nerdctl: %v", got, want)
	}
	return nil
}

// foreignFlagsRE matches Use strings such as "run [flags] IMAGE [COMMAND]
// [ARG...]" where trailing arguments belong to the container command and
// option parsing must stop at the first positional argument.
var foreignFlagsRE = regexp.MustCompile(`COMMAND\]? \[ARGS?\.\.\.\]`)

func walk(cmd *cobra.Command, path string, specs map[string]*commandSpec) {
	spec := &commandSpec{
		name:         cmd.Name(),
		short:        cmd.Short,
		foreignFlags: foreignFlagsRE.MatchString(cmd.Use),
		flagUsage:    map[string]string{},
	}
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		names := []string{"--" + f.Name}
		if f.Shorthand != "" {
			names = append(names, "-"+f.Shorthand)
		}
		if f.NoOptDefVal == "" {
			spec.valueFlags = append(spec.valueFlags, names...)
			for _, name := range names {
				spec.flagUsage[name] = f.Usage
			}
		} else {
			spec.boolFlags = append(spec.boolFlags, names...)
		}
	})
	for _, sub := range cmd.Commands() {
		subPath := joinPath(path, sub.Name())
		spec.subcommands = append(spec.subcommands, sub.Name())
		walk(sub, subPath, specs)
	}
	slices.Sort(spec.valueFlags)
	slices.Sort(spec.boolFlags)
	slices.Sort(spec.subcommands)
	specs[path] = spec
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + " " + name
}

// detectAliases finds command paths that reach the same command definition
// and keeps only the deepest as canonical. nerdctl registers e.g. `run` both
// at the root and under `container` by calling the same constructor twice,
// so an alias has an identical name, Short text, and shape. The Short text
// distinguishes same-shaped commands from different constructors, such as
// `container kill` and `compose kill`.
func detectAliases(specs map[string]*commandSpec) map[string]string {
	aliases := map[string]string{}
	byFingerprint := map[string][]string{}
	for path, spec := range specs {
		if path == "" {
			continue
		}
		fp := fmt.Sprintf("%s|%s|%v|%v|%v|%v", spec.name, spec.short, spec.valueFlags, spec.boolFlags, spec.subcommands, spec.foreignFlags)
		byFingerprint[fp] = append(byFingerprint[fp], path)
	}
	for _, paths := range byFingerprint {
		if len(paths) < 2 {
			continue
		}
		// The deepest path wins so `run` resolves to `container run`; on
		// equal depth the lexicographically first wins so `builder build`
		// beats `image build`.
		slices.SortFunc(paths, func(a, b string) int {
			if d := strings.Count(a, " ") - strings.Count(b, " "); d != 0 {
				return d
			}
			return strings.Compare(b, a)
		})
		canonical := paths[len(paths)-1]
		for _, alias := range paths[:len(paths)-1] {
			aliases[alias] = canonical
			delete(specs, alias)
		}
	}
	return aliases
}

// overlayData holds the command/flag names parsed out of overlay.go.
type overlayData struct {
	pathFlags    map[string][]string
	notPathFlags map[string][]string
}

// loadOverlay reads the pathFlags and notPathFlags map keys from overlay.go
// with go/ast. Parsing instead of importing keeps this module's dependency
// graph independent of the main rdd module.
func loadOverlay(filename string) (*overlayData, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return nil, err
	}
	overlay := &overlayData{pathFlags: map[string][]string{}, notPathFlags: map[string][]string{}}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || len(valueSpec.Names) != 1 || len(valueSpec.Values) != 1 {
				continue
			}
			lit, ok := valueSpec.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			var into map[string][]string
			switch valueSpec.Names[0].Name {
			case "pathFlags":
				into = overlay.pathFlags
			case "notPathFlags":
				into = overlay.notPathFlags
			default:
				continue
			}
			if err := readOverlayMap(lit, into); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", filename, valueSpec.Names[0].Name, err)
			}
		}
	}
	if len(overlay.pathFlags) == 0 {
		return nil, fmt.Errorf("%s: no pathFlags entries found", filename)
	}
	return overlay, nil
}

// readOverlayMap flattens one overlay map literal to command path → flag
// names; the inner value may be a map literal (pathFlags) or a []string
// literal (notPathFlags).
func readOverlayMap(lit *ast.CompositeLit, into map[string][]string) error {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return fmt.Errorf("unexpected element %T", elt)
		}
		command, err := stringLit(kv.Key)
		if err != nil {
			return err
		}
		inner, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			return fmt.Errorf("%q: unexpected value %T", command, kv.Value)
		}
		for _, innerElt := range inner.Elts {
			var flagExpr ast.Expr = innerElt
			if innerKV, ok := innerElt.(*ast.KeyValueExpr); ok {
				flagExpr = innerKV.Key
			}
			name, err := stringLit(flagExpr)
			if err != nil {
				return fmt.Errorf("%q: %w", command, err)
			}
			into[command] = append(into[command], name)
		}
	}
	return nil
}

func stringLit(expr ast.Expr) (string, error) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", fmt.Errorf("expected string literal, got %T", expr)
	}
	return strconv.Unquote(lit.Value)
}

// pathNameRE spots flag names that suggest the value contains a path.
var pathNameRE = regexp.MustCompile(`(?i)(file|path|dir|output|input|volume|mount|context|device|sock|key)`)

// pathUsageRE spots usage text that suggests the value contains a path.
var pathUsageRE = regexp.MustCompile(`(?i)(path|file|director)`)

// validateOverlay checks the overlay both ways: every overlay entry must
// exist in the tree as a value-consuming flag, and every value-consuming
// flag that smells like a path must be classified.
func validateOverlay(specs map[string]*commandSpec, aliases map[string]string, overlay *overlayData) error {
	var problems []string
	classified := map[string]map[string]bool{}
	for _, m := range []map[string][]string{overlay.pathFlags, overlay.notPathFlags} {
		for command, flags := range m {
			spec, ok := specs[command]
			if !ok {
				if canonical, isAlias := aliases[command]; isAlias {
					problems = append(problems, fmt.Sprintf("overlay entry %q is an alias; use %q", command, canonical))
				} else {
					problems = append(problems, fmt.Sprintf("overlay entry %q: no such command", command))
				}
				continue
			}
			for _, name := range flags {
				if !slices.Contains(spec.valueFlags, name) {
					problems = append(problems, fmt.Sprintf("overlay entry %q %s: no such value flag", command, name))
					continue
				}
				if classified[command] == nil {
					classified[command] = map[string]bool{}
				}
				classified[command][name] = true
			}
		}
	}
	for _, command := range slices.Sorted(maps.Keys(specs)) {
		spec := specs[command]
		for _, name := range spec.valueFlags {
			if classified[command][name] {
				continue
			}
			if !pathNameRE.MatchString(name) && !pathUsageRE.MatchString(spec.flagUsage[name]) {
				continue
			}
			problems = append(problems, fmt.Sprintf("unclassified path-smelling flag: %q %s (add to pathFlags or notPathFlags in overlay.go)", command, name))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("overlay validation failed:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

func emit(output, version string, specs map[string]*commandSpec, aliases map[string]string) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "// Code generated by ./generate from %s@%s; DO NOT EDIT.\n\n", nerdctlModule, version)
	buf.WriteString("package nerdctlstub\n\n")
	fmt.Fprintf(&buf, "// nerdctlVersion is the version the parse table was generated from.\nconst nerdctlVersion = %q\n\n", version)
	buf.WriteString("// commandSpecs describes every nerdctl command; the key is the\n")
	buf.WriteString("// space-separated command path, with \"\" for the root command.\n")
	buf.WriteString("var commandSpecs = map[string]commandSpec{\n")
	for _, path := range slices.Sorted(maps.Keys(specs)) {
		spec := specs[path]
		fmt.Fprintf(&buf, "\t%q: {\n", path)
		writeStringList(&buf, "valueFlags", spec.valueFlags)
		writeStringList(&buf, "boolFlags", spec.boolFlags)
		writeStringList(&buf, "subcommands", spec.subcommands)
		if spec.foreignFlags {
			buf.WriteString("\t\tforeignFlags: true,\n")
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n\n")
	buf.WriteString("// commandAliases maps every alias path to its canonical command path.\n")
	buf.WriteString("var commandAliases = map[string]string{\n")
	for _, alias := range slices.Sorted(maps.Keys(aliases)) {
		fmt.Fprintf(&buf, "\t%q: %q,\n", alias, aliases[alias])
	}
	buf.WriteString("}\n")
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("formatting %s: %w", output, err)
	}
	if err := os.WriteFile(output, formatted, 0o644); err != nil {
		return err
	}
	log.Printf("wrote %s", output)
	return nil
}

func writeStringList(buf *bytes.Buffer, field string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(buf, "\t\t%s: []string{", field)
	for i, v := range values {
		if i > 0 {
			buf.WriteString(", ")
		}
		fmt.Fprintf(buf, "%q", v)
	}
	buf.WriteString("},\n")
}
