package loop

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// A STRUCTURAL GUARD FOR A SILENT BUG CLASS.
//
// Runner.OnProtect was declared, documented, and assigned by the orchestrator —
// and never invoked. SnapshotProtected therefore never ran, no protected file
// ever got a backup, and workspace.healProtectedWrites could only report
// violations rather than undo them.
//
// Nothing caught it. The workspace tests called SnapshotProtected directly, the
// orchestrator test asserted the field was assigned, the compiler was satisfied
// because assigning a func field is a use, and the live scenario that would have
// exercised it passed for an unrelated reason. A callback that is declared,
// wired and never called is invisible to every ordinary test.
//
// This reads the package's own syntax tree instead: every exported callback
// field on Runner must appear as a CALL somewhere in package loop. It cannot
// prove the call happens at the right moment — protecthook_test.go does that —
// but it makes "wired to nothing" impossible to land again.
func TestEveryRunnerCallbackIsActuallyInvoked(t *testing.T) {
	// Production sources only: a hook invoked exclusively from a test is exactly
	// the defect this is looking for.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no production sources parsed — the detector is broken, not the code")
	}

	hooks := callbackFields(files, "Runner")
	if len(hooks) == 0 {
		t.Fatal("found no callback fields on Runner — the detector is broken, " +
			"not the code")
	}
	called := calledNames(files)

	for _, h := range hooks {
		if !called[h] {
			t.Errorf("Runner.%s is a declared callback that package loop never "+
				"CALLS. Assigning it compiles and looks wired, so nothing else "+
				"fails — but the behavior behind it never runs. Either invoke "+
				"it or delete it.", h)
		}
	}
}

// callbackFields returns the names of exported func-typed fields on the named
// struct — the hook seams a caller can assign.
func callbackFields(files []*ast.File, structName string) []string {
	var out []string
	inspectAll(files, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != structName {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, f := range st.Fields.List {
			if !isFuncType(f.Type) {
				continue
			}
			for _, name := range f.Names {
				if name.IsExported() {
					out = append(out, name.Name)
				}
			}
		}
		return false
	})
	return out
}

// isFuncType reports a field whose type is a function — either written inline
// (`func(...)`) or a named function type declared in this package.
func isFuncType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.FuncType:
		return true
	case *ast.Ident:
		// A named type like `BetweenWaves`; resolve through its declaration.
		if t.Obj != nil {
			if ts, ok := t.Obj.Decl.(*ast.TypeSpec); ok {
				_, isFunc := ts.Type.(*ast.FuncType)
				return isFunc
			}
		}
	}
	return false
}

// calledNames collects every selector invoked as a function, so `r.OnProtect(x)`
// registers OnProtect as called.
func calledNames(files []*ast.File) map[string]bool {
	out := map[string]bool{}
	inspectAll(files, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			out[sel.Sel.Name] = true
		}
		return true
	})
	return out
}

// inspectAll walks every parsed file with one visitor.
func inspectAll(files []*ast.File, fn func(ast.Node) bool) {
	for _, f := range files {
		ast.Inspect(f, fn)
	}
}
