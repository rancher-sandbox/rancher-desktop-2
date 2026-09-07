// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Extract is stage 1 of the nerdctl parse-table generator. nerdctl's root
// command lives in package main and cannot be imported, so this tool parses
// cmd/nerdctl/main.go and main_linux.go from the pinned module with go/ast
// and writes two files into the generator package:
//
//   - zz_generated_commands.go: the subcommand constructor calls that newApp
//     passes to AddCommand, so a nerdctl bump picks up new subcommands
//     without manual work.
//   - zz_generated_rootflags.go: the flag names that initRootCmdFlags
//     registers, so stage 2 can verify the hand-written replica in
//     replica.go against upstream.
//
// Any unrecognized registration shape in those files is a fatal error;
// failing loudly here is what keeps the replica honest across bumps.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const nerdctlModule = "github.com/containerd/nerdctl/v2"

// constructorCall is one `pkg.Func()` argument of an AddCommand call.
type constructorCall struct {
	pkgName string // import name, e.g. "container"
	pkgPath string // import path
	funName string // e.g. "CreateCommand"
}

// extracted holds everything harvested from the nerdctl main package.
type extracted struct {
	version      string
	constructors []constructorCall
	addFuncs     []constructorCall // pkg.AddXxxCommand(rootCmd) helpers
	unresolved   []string          // package-main constructors, e.g. versionCommand
	skipped      []string          // constructors in internal packages
	rootFlags    []string          // flag names registered by initRootCmdFlags
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("extract: ")
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	modDir, version, err := nerdctlModuleInfo()
	if err != nil {
		return err
	}
	ex := &extracted{version: version}
	for _, name := range []string{"main.go", "main_linux.go"} {
		if err := ex.parseFile(filepath.Join(modDir, "cmd", "nerdctl", name)); err != nil {
			return err
		}
	}
	if len(ex.constructors) == 0 {
		return fmt.Errorf("no AddCommand constructors found; did the nerdctl main package move?")
	}
	if len(ex.rootFlags) == 0 {
		return fmt.Errorf("no root flags found; did initRootCmdFlags move?")
	}
	if err := ex.writeCommands("zz_generated_commands.go"); err != nil {
		return err
	}
	return ex.writeRootFlags("zz_generated_rootflags.go")
}

// nerdctlModuleInfo resolves the pinned module's source directory and version
// through the enclosing generator module.
func nerdctlModuleInfo() (dir, version string, err error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}} {{.Version}}", nerdctlModule).Output()
	if err != nil {
		return "", "", fmt.Errorf("go list -m %s: %w", nerdctlModule, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return "", "", fmt.Errorf("unexpected go list output %q", out)
	}
	return fields[0], fields[1], nil
}

func (ex *extracted) parseFile(filename string) error {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return err
	}
	imports := importMap(file)
	var inspectErr error
	ast.Inspect(file, func(n ast.Node) bool {
		if inspectErr != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "AddCommand" {
			inspectErr = ex.collectConstructors(call, imports)
			return true
		}
		// pkg.AddXxxCommand(rootCmd) helpers, e.g. container.AddCpCommand.
		if recv, ok := sel.X.(*ast.Ident); ok {
			if pkgPath, isImport := imports[recv.Name]; isImport &&
				strings.HasPrefix(sel.Sel.Name, "Add") && strings.HasSuffix(sel.Sel.Name, "Command") {
				ex.addFuncs = append(ex.addFuncs, constructorCall{recv.Name, pkgPath, sel.Sel.Name})
			}
		}
		return true
	})
	if inspectErr != nil {
		return fmt.Errorf("%s: %w", filename, inspectErr)
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "initRootCmdFlags" {
			if err := ex.collectRootFlags(fn); err != nil {
				return fmt.Errorf("%s: %w", filename, err)
			}
		}
	}
	return nil
}

// importMap maps the name each import is used under to its path.
func importMap(file *ast.File) map[string]string {
	imports := map[string]string{}
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = p
	}
	return imports
}

// collectConstructors records each argument of an AddCommand call. Arguments
// must be direct constructor calls; anything else means newApp changed shape
// and this tool needs updating.
func (ex *extracted) collectConstructors(call *ast.CallExpr, imports map[string]string) error {
	for _, arg := range call.Args {
		argCall, ok := arg.(*ast.CallExpr)
		if !ok {
			return fmt.Errorf("AddCommand argument %s is not a constructor call", types(arg))
		}
		switch fun := argCall.Fun.(type) {
		case *ast.SelectorExpr:
			pkg, ok := fun.X.(*ast.Ident)
			if !ok {
				return fmt.Errorf("AddCommand argument has unexpected receiver %s", types(fun.X))
			}
			pkgPath, ok := imports[pkg.Name]
			if !ok {
				return fmt.Errorf("AddCommand argument references unknown package %q", pkg.Name)
			}
			// Go forbids importing another module's internal packages.
			// Commands missing from the table pass through unparsed, which
			// is right for nerdctl's self-invocation surface.
			if strings.Contains(pkgPath, "/internal") {
				ex.skipped = append(ex.skipped, pkg.Name+"."+fun.Sel.Name)
				continue
			}
			ex.constructors = append(ex.constructors, constructorCall{pkg.Name, pkgPath, fun.Sel.Name})
		case *ast.Ident:
			// A package-main constructor; replica.go must provide it.
			ex.unresolved = append(ex.unresolved, fun.Name)
		default:
			return fmt.Errorf("AddCommand argument has unexpected callee %s", types(argCall.Fun))
		}
	}
	return nil
}

// pflagConstructors are the FlagSet methods whose first argument is the flag
// name, as used on rootCmd.PersistentFlags() in initRootCmdFlags.
var pflagConstructors = map[string]bool{
	"Bool": true, "BoolP": true,
	"String": true, "StringP": true,
	"StringSlice": true, "StringSliceP": true,
	"StringArray": true, "StringArrayP": true,
	"Int": true, "IntP": true,
	"Duration": true, "DurationP": true,
}

// helperNameArg gives the index of the flag-name argument for each
// cmd/nerdctl/helpers registration function, and the indexes of []string
// alias arguments (every alias is registered as a flag in its own right).
var helperNameArg = map[string]struct {
	name    int
	aliases []int
}{
	"AddPersistentStringFlag":         {name: 1, aliases: []int{2, 3, 4}},
	"AddPersistentBoolFlag":           {name: 1, aliases: []int{2, 3}},
	"AddPersistentStringArrayFlag":    {name: 1, aliases: []int{2, 3}},
	"HiddenPersistentStringArrayFlag": {name: 1},
}

// collectRootFlags records every flag name that initRootCmdFlags registers.
// A call containing "Flag" in its name that matches no known shape is fatal,
// so new upstream registration patterns surface instead of being dropped.
func (ex *extracted) collectRootFlags(fn *ast.FuncDecl) error {
	var inspectErr error
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if inspectErr != nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pflagConstructors[sel.Sel.Name] {
			if len(call.Args) == 0 {
				inspectErr = fmt.Errorf("%s: no arguments", sel.Sel.Name)
				return false
			}
			name, err := stringLit(call.Args[0])
			if err != nil {
				inspectErr = fmt.Errorf("%s: %w", sel.Sel.Name, err)
				return false
			}
			ex.rootFlags = append(ex.rootFlags, name)
			return true
		}
		if spec, ok := helperNameArg[sel.Sel.Name]; ok {
			if len(call.Args) <= slices.Max(append(slices.Clone(spec.aliases), spec.name)) {
				inspectErr = fmt.Errorf("%s: fewer arguments than expected", sel.Sel.Name)
				return false
			}
			name, err := stringLit(call.Args[spec.name])
			if err != nil {
				inspectErr = fmt.Errorf("%s: %w", sel.Sel.Name, err)
				return false
			}
			ex.rootFlags = append(ex.rootFlags, name)
			for _, idx := range spec.aliases {
				aliases, err := stringListLit(call.Args[idx])
				if err != nil {
					inspectErr = fmt.Errorf("%s aliases: %w", sel.Sel.Name, err)
					return false
				}
				ex.rootFlags = append(ex.rootFlags, aliases...)
			}
			return true
		}
		switch sel.Sel.Name {
		case "RegisterFlagCompletionFunc", "NewFlagSet",
			"Flags", "PersistentFlags", "LocalFlags":
			return true // registers no flag itself
		}
		if strings.Contains(sel.Sel.Name, "Flag") {
			inspectErr = fmt.Errorf("unhandled flag registration %s in initRootCmdFlags", sel.Sel.Name)
			return false
		}
		return true
	})
	return inspectErr
}

// stringLit returns the value of a string literal expression.
func stringLit(expr ast.Expr) (string, error) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", fmt.Errorf("expected string literal, got %s", types(expr))
	}
	return strconv.Unquote(lit.Value)
}

// stringListLit returns the elements of a []string{...} literal, or nothing
// for a nil argument.
func stringListLit(expr ast.Expr) ([]string, error) {
	if ident, ok := expr.(*ast.Ident); ok && ident.Name == "nil" {
		return nil, nil
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("expected []string literal or nil, got %s", types(expr))
	}
	var elems []string
	for _, e := range lit.Elts {
		s, err := stringLit(e)
		if err != nil {
			return nil, err
		}
		elems = append(elems, s)
	}
	return elems, nil
}

func types(expr ast.Expr) string {
	return fmt.Sprintf("%T", expr)
}

func (ex *extracted) writeCommands(filename string) error {
	pkgPaths := map[string]string{}
	for _, c := range slices.Concat(ex.constructors, ex.addFuncs) {
		pkgPaths[c.pkgName] = c.pkgPath
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "// Code generated by ./extract from %s@%s; DO NOT EDIT.\n\n", nerdctlModule, ex.version)
	buf.WriteString("//go:build linux\n\n")
	buf.WriteString("package main\n\nimport (\n\t\"github.com/spf13/cobra\"\n\n")
	for _, name := range slices.Sorted(maps.Keys(pkgPaths)) {
		fmt.Fprintf(&buf, "\t%q\n", pkgPaths[name])
	}
	buf.WriteString(")\n\n")
	buf.WriteString("// addExtractedCommands adds every subcommand that nerdctl's newApp adds.\n")
	buf.WriteString("func addExtractedCommands(rootCmd *cobra.Command) {\n\trootCmd.AddCommand(\n")
	for _, c := range ex.constructors {
		fmt.Fprintf(&buf, "\t\t%s.%s(),\n", c.pkgName, c.funName)
	}
	buf.WriteString("\t)\n")
	for _, c := range ex.addFuncs {
		fmt.Fprintf(&buf, "\t%s.%s(rootCmd)\n", c.pkgName, c.funName)
	}
	buf.WriteString("}\n\n")
	buf.WriteString("// unresolvedCommands are package-main constructors that replica.go provides.\n")
	fmt.Fprintf(&buf, "var unresolvedCommands = %#v\n\n", ex.unresolved)
	buf.WriteString("// skippedCommands are constructors in internal packages; their commands\n")
	buf.WriteString("// stay out of the table and pass through the parser unmodified.\n")
	fmt.Fprintf(&buf, "var skippedCommands = %#v\n", ex.skipped)
	return writeFormatted(filename, buf.Bytes())
}

func (ex *extracted) writeRootFlags(filename string) error {
	flags := slices.Clone(ex.rootFlags)
	slices.Sort(flags)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "// Code generated by ./extract from %s@%s; DO NOT EDIT.\n\n", nerdctlModule, ex.version)
	buf.WriteString("//go:build linux\n\n")
	buf.WriteString("package main\n\n")
	buf.WriteString("// nerdctlPinnedVersion is the module version extract ran against.\n")
	fmt.Fprintf(&buf, "const nerdctlPinnedVersion = %q\n\n", ex.version)
	buf.WriteString("// expectedRootFlags are the flag names initRootCmdFlags registers;\n")
	buf.WriteString("// replica.go must register exactly these.\n")
	buf.WriteString("var expectedRootFlags = []string{\n")
	for _, name := range flags {
		fmt.Fprintf(&buf, "\t%q,\n", name)
	}
	buf.WriteString("}\n")
	return writeFormatted(filename, buf.Bytes())
}

func writeFormatted(filename string, src []byte) error {
	formatted, err := format.Source(src)
	if err != nil {
		return fmt.Errorf("formatting %s: %w", filename, err)
	}
	if err := os.WriteFile(filename, formatted, 0o644); err != nil {
		return err
	}
	log.Printf("wrote %s", filename)
	return nil
}
