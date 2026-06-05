package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestMethodViolations exercises the §9.3 guard logic directly so the rules it
// enforces over the SPI packages (now including pkg/store and pkg/layer/source)
// are themselves covered: a parameterized method without a leading
// context.Context, a func-typed parameter, and a channel-typed parameter must
// each be flagged, while a zero-parameter accessor and a context-first method
// must pass.
//
// spec: §9.3 SPI wire-compatibility constraints.
func TestMethodViolations(t *testing.T) {
	t.Parallel()
	const src = `package p
import "context"
type Bad interface {
	NoCtx(id string) error
	FuncParam(ctx context.Context, cb func()) error
	ChanParam(ctx context.Context, ch chan int) error
}
type Good interface {
	Accessor() string
	WithCtx(ctx context.Context, id string) error
}`

	check := func(iface, method string, wantViolation bool, wantSubstr string) {
		t.Helper()
		fset, ft := parseIfaceMethod(t, src, iface, method)
		got := methodViolations(fset, "pkg/synthetic", iface, method, ft)
		if wantViolation {
			if len(got) == 0 {
				t.Errorf("%s.%s: expected a violation, got none", iface, method)
				return
			}
			if wantSubstr != "" && !strings.Contains(strings.Join(got, "\n"), wantSubstr) {
				t.Errorf("%s.%s: violations %v do not mention %q", iface, method, got, wantSubstr)
			}
		} else if len(got) != 0 {
			t.Errorf("%s.%s: expected no violation, got %v", iface, method, got)
		}
	}

	check("Bad", "NoCtx", true, "context.Context")
	check("Bad", "FuncParam", true, "func type")
	check("Bad", "ChanParam", true, "channel type")
	check("Good", "Accessor", false, "")
	check("Good", "WithCtx", false, "")
}

// parseIfaceMethod parses src and returns the FuncType of ifaceName.methodName.
func parseIfaceMethod(t *testing.T, src, ifaceName, methodName string) (*token.FileSet, *ast.FuncType) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var ft *ast.FuncType
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != ifaceName {
			return true
		}
		iface, ok := ts.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}
		for _, m := range iface.Methods.List {
			if len(m.Names) > 0 && m.Names[0].Name == methodName {
				if fn, ok := m.Type.(*ast.FuncType); ok {
					ft = fn
				}
			}
		}
		return true
	})
	if ft == nil {
		t.Fatalf("method %s.%s not found", ifaceName, methodName)
	}
	return fset, ft
}
