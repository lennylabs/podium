package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSPISignatures is the §9.3 SPI wire-compatibility guard required by the
// §11 verification item ("Static analysis [that] fails the build on
// regressions"). It walks the exported interface method sets of the built-in
// SPI packages and fails when a method violates the §9.3 rule:
//
//   - No parameter may be a func type or a channel type (these cannot cross a
//     process boundary).
//   - Any method that takes parameters must take a leading context.Context, so
//     an out-of-process implementation can honor deadlines and cancellation.
//
// Zero-parameter accessors (for example ID, Type, Code) are exempt from the
// context rule because they perform no work. An exported SPI declared as a
// bare func type is rejected outright; the "...Func" adapter idiom (an
// in-process convenience that wraps a function into the interface) is exempt.
//
// spec: §9.3 SPI Catalogue; spec/11-verification.md SPI wire-compatibility
// test. Guards F-9.3.1 (hook), F-9.3.2 (sign), F-9.3.3 (adapter), F-9.3.4
// (lint, typeprovider).
func TestSPISignatures(t *testing.T) {
	// SPI packages, relative to the repository root.
	spiPkgs := []string{
		"pkg/adapter",
		"pkg/sign",
		"pkg/hook",
		"pkg/lint",
		"pkg/typeprovider",
	}

	root := spiRepoRoot(t)
	fset := token.NewFileSet()

	checked := 0
	for _, pkg := range spiPkgs {
		dir := filepath.Join(root, pkg)
		pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", pkg, err)
		}
		for _, p := range pkgs {
			for _, file := range p.Files {
				ast.Inspect(file, func(n ast.Node) bool {
					ts, ok := n.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						return true
					}
					// An SPI declared as a bare func type cannot cross a
					// process boundary. The "...Func" adapter idiom is an
					// in-process convenience and is exempt.
					if _, isFunc := ts.Type.(*ast.FuncType); isFunc {
						if !strings.HasSuffix(ts.Name.Name, "Func") {
							t.Errorf("%s: exported SPI type %s is a func type, which §9.3 forbids; define it as an interface",
								pkg, ts.Name.Name)
						}
						return true
					}
					iface, ok := ts.Type.(*ast.InterfaceType)
					if !ok {
						return true
					}
					for _, m := range iface.Methods.List {
						ft, ok := m.Type.(*ast.FuncType)
						if !ok || len(m.Names) == 0 {
							continue // embedded interface, not a method
						}
						checked++
						checkMethod(t, fset, pkg, ts.Name.Name, m.Names[0].Name, ft)
					}
					return true
				})
			}
		}
	}

	if checked == 0 {
		t.Fatal("no SPI interface methods were inspected; check the package list")
	}
}

// checkMethod enforces the §9.3 parameter rules on a single interface method.
func checkMethod(t *testing.T, fset *token.FileSet, pkg, iface, method string, ft *ast.FuncType) {
	t.Helper()

	var params []*ast.Field
	if ft.Params != nil {
		params = ft.Params.List
	}

	// Rule: no func-typed or channel-typed parameters.
	for _, p := range params {
		switch p.Type.(type) {
		case *ast.FuncType:
			t.Errorf("%s.%s: parameter has a func type, which §9.3 forbids (cannot cross a process boundary) at %s",
				iface, method, fset.Position(p.Pos()))
		case *ast.ChanType:
			t.Errorf("%s.%s: parameter has a channel type, which §9.3 forbids at %s",
				iface, method, fset.Position(p.Pos()))
		}
	}

	// Rule: a method with parameters must take a leading context.Context.
	if len(params) == 0 {
		return // accessor; exempt
	}
	if !isContextContext(params[0].Type) {
		t.Errorf("%s.%s in %s: first parameter is not context.Context; §9.3 requires a context-first signature",
			iface, method, pkg)
	}
}

// isContextContext reports whether expr is the type context.Context.
func isContextContext(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "context" && sel.Sel.Name == "Context"
}

// repoRoot returns the repository root by walking up from this source file
// (test/conformance/spi_signatures_test.go).
func spiRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}
