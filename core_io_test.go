package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// consoleFrontEnd lists the functions that ARE the console front end (menus,
// CLI, console rendering). Everything else in the checked files is core and
// must report through a.ui only (doc/UI_SPEC_RU.md §6).
var consoleFrontEnd = map[string]bool{
	// main.go
	"realMain": true, "run": true, "showErr": true, "expertMenu": true, "bold": true,
	// main_probe.go
	"cliProbe": true, "cliExport": true, "portingMenu": true, "showProbeErr": true,
	"menuProbe": true, "menuBootROM": true, "menuUBIAttach": true, "menuExport": true,
	"menuView": true, "printProbeSummary": true,
	// console_ui.go drawing helpers
	"consoleColorEnabled": true, "paint": true, "uiRule": true, "uiStatus": true,
	"eventTone": true, "uiEvent": true,
}

// TestCoreHasNoDirectConsoleIO keeps the core free of fmt.Print*, os.Stdout,
// console drawing helpers and direct stdin reads.
func TestCoreHasNoDirectConsoleIO(t *testing.T) {
	forbidden := map[string]bool{"uiRule": true, "uiStatus": true, "uiEvent": true}
	fset := token.NewFileSet()
	for _, file := range []string{"main.go", "main_probe.go", "stock_access.go", "console_ui.go"} {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || consoleFrontEnd[fd.Name.Name] {
				continue
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok {
						if id.Name == "fmt" && (x.Sel.Name == "Print" || x.Sel.Name == "Println" || x.Sel.Name == "Printf") {
							t.Errorf("%s: core function %s calls fmt.%s; use a.ui", fset.Position(x.Pos()), fd.Name.Name, x.Sel.Name)
						}
						if id.Name == "os" && (x.Sel.Name == "Stdout" || x.Sel.Name == "Stdin") {
							t.Errorf("%s: core function %s uses os.%s; use a.ui", fset.Position(x.Pos()), fd.Name.Name, x.Sel.Name)
						}
					}
					if x.Sel.Name == "reader" {
						t.Errorf("%s: core function %s reads the console directly; use a.ui.Ask", fset.Position(x.Pos()), fd.Name.Name)
					}
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok && forbidden[id.Name] {
						t.Errorf("%s: core function %s draws console output (%s); use a.ui", fset.Position(x.Pos()), fd.Name.Name, id.Name)
					}
				}
				return true
			})
		}
	}
}
