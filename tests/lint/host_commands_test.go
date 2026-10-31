package lint

import (
	"go/ast"
	"go/token"
	"go/types"
	"testing"
)

// Server maintenance reads the machine through services.Host, which tests
// swap, and runs only fixed commands (ADR 0041). A process started
// anywhere else in services would run on the developer's own machine in
// tests, and might one day take something a request sent.
func TestOnlyTheHostRunnerStartsProcessesInServices(t *testing.T) {
	pkgs, err := source()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[token.Position]bool{}
	for _, pkg := range pkgs {
		if pkg.PkgPath != "goravel/app/services" {
			continue
		}
		for _, file := range pkg.Syntax {
			var runner *ast.FuncDecl
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "runOnHost" {
					runner = fn
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || runner != nil && call.Pos() >= runner.Pos() && call.End() <= runner.End() {
					return true
				}
				pos := pkg.Fset.Position(call.Pos())
				if fn := calledFunc(pkg.TypesInfo, call); fn != nil && (fn.FullName() == "os/exec.Command" || fn.FullName() == "os/exec.CommandContext") && !seen[pos] {
					seen[pos] = true
					t.Errorf("%s: read the machine through services.Host, whose Run (runOnHost) is the only place services starts a process", pos)
				}
				return true
			})
		}
	}
}

func calledFunc(info *types.Info, call *ast.CallExpr) *types.Func {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	fn, _ := info.Uses[selector.Sel].(*types.Func)

	return fn
}
