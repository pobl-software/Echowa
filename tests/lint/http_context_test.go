// Package lint checks Echowa's own source for mistakes the compiler allows.
package lint

import (
	"go/ast"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

// Goravel pools its http.Context: once a handler returns, the next request
// reuses it. Anything that keeps a context past the handler, such as
// database/sql watching a query's rows or a goroutine the request starts,
// then reads another request's state. It implements context.Context, so
// the compiler takes it as one. Pass ctx.Context(), the request's own.
func TestHTTPContextIsNeverPassedAsAContextContext(t *testing.T) {
	pkgs, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:   "../..",
		Tests: true,
	}, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("the packages don't type-check")
	}

	seen := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sig, ok := pkg.TypesInfo.TypeOf(call.Fun).(*types.Signature)
				if !ok {
					return true
				}
				for i, arg := range call.Args {
					if !isNamed(param(sig, i), "context", "Context") || !isNamed(pkg.TypesInfo.TypeOf(arg), "github.com/goravel/framework/contracts/http", "Context") {
						continue
					}
					// A package and its tests load twice.
					pos := pkg.Fset.Position(arg.Pos()).String()
					if !seen[pos] {
						seen[pos] = true
						t.Errorf("%s: pass %s.Context(), not Goravel's pooled http.Context, as a context.Context", pos, types.ExprString(arg))
					}
				}
				return true
			})
		}
	}
}

// param is the type of a call's ith argument, the variadic element's past
// the last parameter.
func param(sig *types.Signature, i int) types.Type {
	params := sig.Params()
	if sig.Variadic() && i >= params.Len()-1 {
		if slice, ok := params.At(params.Len() - 1).Type().(*types.Slice); ok {
			return slice.Elem()
		}
	}
	if i >= params.Len() {
		return nil
	}

	return params.At(i).Type()
}

func isNamed(t types.Type, pkg, name string) bool {
	named, ok := t.(*types.Named)

	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == pkg && named.Obj().Name() == name
}
