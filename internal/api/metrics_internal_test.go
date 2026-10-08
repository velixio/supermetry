package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestMetricsTokenUsesConstantTimeCompare(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "metrics.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "ConstantTimeCompare" {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		found = ok && packageName.Name == "subtle"
		return !found
	})
	if !found {
		t.Fatal("metrics token authentication must use subtle.ConstantTimeCompare")
	}
}
