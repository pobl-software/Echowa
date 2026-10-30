// Package lint checks Echowa's own source for mistakes the compiler allows.
package lint

import (
	"errors"
	"go/ast"
	"go/token"
	"go/types"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"
)

const httpContracts = "github.com/goravel/framework/contracts/http"

// Goravel pools its http.Context: once a handler returns, the next request
// reuses it. Anything that keeps a context past the handler, such as
// database/sql watching a query's rows or a goroutine the request starts,
// then reads another request's state. It implements context.Context, so
// the compiler takes it as one. Pass ctx.Context(), the request's own.
func TestHTTPContextIsNeverPassedAsAContextContext(t *testing.T) {
	eachCall(t, func(info *types.Info, call *ast.CallExpr, pos token.Position) {
		sig, ok := info.TypeOf(call.Fun).(*types.Signature)
		if !ok {
			return
		}
		for i, arg := range call.Args {
			if isNamed(param(sig, i), "context", "Context") && isNamed(info.TypeOf(arg), httpContracts, "Context") {
				t.Errorf("%s: pass %s.Context(), not Goravel's pooled http.Context, as a context.Context", pos, types.ExprString(arg))
			}
		}
	})
}

// pooledQueries are Goravel's own calls that query the database with the
// pooled http.Context they were given, and what Echowa calls instead.
// Validate hands it to the unique and exists rules.
var pooledQueries = map[string]string{
	"(" + httpContracts + ".ContextRequest).Validate":                "rules.Validate(ctx, ...)",
	"(" + httpContracts + ".ContextRequest).ValidateRequest":         "rules.Validate(ctx, ...)",
	"(github.com/goravel/framework/contracts/auth.GuardDriver).User": "services.CurrentUser(ctx)",
}

func TestGoravelNeverQueriesWithItsPooledContext(t *testing.T) {
	eachCall(t, func(info *types.Info, call *ast.CallExpr, pos token.Position) {
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		fn, ok := info.Uses[selector.Sel].(*types.Func)
		if !ok {
			return
		}
		if instead, ok := pooledQueries[fn.FullName()]; ok {
			t.Errorf("%s: call %s, not %s, which queries with Goravel's pooled http.Context", pos, instead, fn.FullName())
		}
	})
}

// source is Echowa's packages and their tests, type-checked once for every
// test here.
var source = sync.OnceValues(func() ([]*packages.Package, error) {
	pkgs, err := packages.Load(&packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:   "../..",
		Tests: true,
	}, "./...")
	if err == nil && packages.PrintErrors(pkgs) > 0 {
		err = errors.New("the packages don't type-check")
	}

	return pkgs, err
})

// eachCall gives check every call in Echowa's source once, though a
// package and its tests load twice.
func eachCall(t *testing.T, check func(info *types.Info, call *ast.CallExpr, pos token.Position)) {
	pkgs, err := source()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[token.Position]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if pos := pkg.Fset.Position(call.Pos()); !seen[pos] {
						seen[pos] = true
						check(pkg.TypesInfo, call, pos)
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
