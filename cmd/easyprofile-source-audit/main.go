// check-source-safety audits Go call expressions, not comments or source substrings.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func auditSource(path string, data []byte) []string {
	positions := token.NewFileSet()
	source, err := parser.ParseFile(positions, path, data, 0)
	if err != nil {
		return []string{err.Error()}
	}
	imports := map[string]string{}
	var findings []string
	report := func(node ast.Node, message string) {
		findings = append(findings, fmt.Sprintf("%s: %s", positions.Position(node.Pos()), message))
	}
	isTest := strings.HasSuffix(path, "_test.go")
	for _, entry := range source.Imports {
		pkg, _ := strconv.Unquote(entry.Path.Value)
		alias := filepath.Base(pkg)
		if entry.Name != nil {
			alias = entry.Name.Name
		}
		imports[alias] = pkg
		if alias == "." {
			report(entry, "dot imports hide audited call targets")
		}
		if isTest && pkg == "os/exec" {
			report(entry, "tests must inject fakes, never spawn host commands")
		}
	}
	ast.Inspect(source, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		pkg, method := imports[receiver.Name], selector.Sel.Name
		if pkg == "os/exec" && (method == "Command" || method == "CommandContext") {
			if method == "Command" {
				report(call, "unbounded subprocess; use CommandContext")
			}
			index := 0
			if method == "CommandContext" {
				index = 1
			}
			if len(call.Args) <= index {
				return true
			}
			literal, ok := call.Args[index].(*ast.BasicLit)
			if !ok {
				if filepath.ToSlash(path) != "internal/inspect/runner.go" {
					report(call, "dynamic executable outside the read-only allowlist runner")
				}
				return true
			}
			name, _ := strconv.Unquote(literal.Value)
			switch filepath.Base(name) {
			case "sh", "bash", "dash", "zsh", "sudo", "rm", "kill", "killall":
				report(call, "forbidden shell/destructive subprocess: "+name)
			}
		}
		if pkg == "os" {
			if isTest && method == "Unsetenv" {
				report(call, "tests must restore their environment with t.Setenv")
			}
			p := filepath.ToSlash(path)
			if !isTest && (strings.HasPrefix(p, "internal/adapters/") || strings.HasPrefix(p, "internal/inspect/")) {
				switch method {
				case "WriteFile", "Create", "CreateTemp", "OpenFile", "Rename", "Remove", "RemoveAll", "Mkdir", "MkdirAll", "Chmod", "Chown", "Truncate":
					report(call, "audio adapters/inspectors must never write preset files")
				}
			}
		}
		return true
	})
	return findings
}

func main() {
	var findings []string
	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			findings = append(findings, auditSource(path, data)...)
			return nil
		})
		if err != nil {
			findings = append(findings, err.Error())
		}
	}
	if len(findings) > 0 {
		for _, finding := range findings {
			fmt.Fprintln(os.Stderr, finding)
		}
		os.Exit(1)
	}
	fmt.Println("Go AST safety audit passed: bounded direct commands, fake-only tests and read-only audio adapters.")
}
