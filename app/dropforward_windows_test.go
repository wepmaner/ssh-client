package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wailsDir is the source folder of the Wails module this build uses.
func wailsDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/wailsapp/wails/v3").Output()
	if err != nil {
		t.Fatalf("go list wails: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatal("no Wails module folder")
	}
	return dir
}

func parseWails(t *testing.T, dir, rel string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, filepath.FromSlash(rel)), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return f
}

// structFields lists the fields of struct name in f as name and type, embedded ones by their type.
func structFields(t *testing.T, f *ast.File, name string) [][2]string {
	t.Helper()
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range g.Specs {
			ts, ok := s.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s is not a struct", name)
			}
			var out [][2]string
			for _, fl := range st.Fields.List {
				typ := types.ExprString(fl.Type)
				if len(fl.Names) == 0 {
					out = append(out, [2]string{typ, typ})
				}
				for _, n := range fl.Names {
					out = append(out, [2]string{n.Name, typ})
				}
			}
			return out
		}
	}
	t.Fatalf("no struct %s", name)
	return nil
}

func fieldType(t *testing.T, f *ast.File, st, field string) string {
	t.Helper()
	for _, fl := range structFields(t, f, st) {
		if fl[0] == field {
			return fl[1]
		}
	}
	t.Fatalf("struct %s has no field %s: the drop target cannot reach the WebView (dropforward_windows.go)", st, field)
	return ""
}

// TestWailsDropFieldPath checks, in the Wails sources, the unexported path dropforward_windows.go
// reads at run time and the vtable slots internal/oledrag calls. A Wails upgrade that changes them
// fails here; then update compositionControllerPath, or drop the code if Wails forwards drops itself.
func TestWailsDropFieldPath(t *testing.T) {
	dir := wailsDir(t)
	app := parseWails(t, dir, "pkg/application/webview_window.go")
	win := parseWails(t, dir, "pkg/application/webview_window_windows.go")
	chromium := parseWails(t, dir, "internal/webview2/pkg/edge/chromium.go")

	// The packages the run-time type strings name.
	if app.Name.Name != "application" || win.Name.Name != "application" || chromium.Name.Name != "edge" {
		t.Fatalf("packages %s %s %s", app.Name.Name, win.Name.Name, chromium.Name.Name)
	}
	if got := fieldType(t, app, "WebviewWindow", "impl"); got != "webviewWindowImpl" {
		t.Fatalf("WebviewWindow.impl is %s", got)
	}
	// impl holds what newWindowImpl makes.
	found := false
	for _, d := range win.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "newWindowImpl" && fn.Recv == nil {
			found = fn.Type.Results != nil && len(fn.Type.Results.List) == 1 &&
				types.ExprString(fn.Type.Results.List[0].Type) == "*windowsWebviewWindow"
		}
	}
	if !found {
		t.Fatal("newWindowImpl no longer returns *windowsWebviewWindow")
	}
	want := map[string]string{
		"*application.windowsWebviewWindow":        "*windowsWebviewWindow",
		"*edge.Chromium":                           "*edge.Chromium",
		"*edge.ICoreWebView2CompositionController": "*ICoreWebView2CompositionController",
	}
	steps := [][3]string{ // file struct, field, the type string compositionControllerPath expects
		{"windowsWebviewWindow", "chromium", "*edge.Chromium"},
		{"Chromium", "compositionController", "*edge.ICoreWebView2CompositionController"},
	}
	if compositionControllerPath[0].typ != "*application.windowsWebviewWindow" {
		t.Fatalf("path starts with %v", compositionControllerPath[0])
	}
	files := map[string]*ast.File{"windowsWebviewWindow": win, "Chromium": chromium}
	for i, s := range steps {
		p := compositionControllerPath[i+1]
		if p.name != s[1] || p.typ != s[2] {
			t.Fatalf("compositionControllerPath[%d] = %v, want %s %s", i+1, p, s[1], s[2])
		}
		if got := fieldType(t, files[s[0]], s[0], s[1]); got != want[s[2]] {
			t.Fatalf("%s.%s is %s, want %s", s[0], s[1], got, want[s[2]])
		}
	}
	if got := fieldType(t, chromium, "Chromium", "CompositionControllerEnabled"); got != "bool" {
		t.Fatalf("CompositionControllerEnabled is %s", got)
	}
	if compositionEnabledPath[2] != (fieldStep{"CompositionControllerEnabled", "bool"}) {
		t.Fatalf("compositionEnabledPath %v", compositionEnabledPath)
	}

	// The CompositionController4 vtable, which Wails calls in production, lays out the inherited
	// Controller3 methods: oledrag calls DragEnter, DragLeave, DragOver, Drop at slots 12–15.
	cc4 := parseWails(t, dir, "internal/webview2/pkg/edge/ICoreWebView2CompositionController4.go")
	slot := map[string]int{}
	n := 0
	for _, f := range structFields(t, cc4, "ICoreWebView2CompositionController4Vtbl") {
		if f[0] == "_IUnknownVtbl" {
			n += 3
			continue
		}
		slot[f[0]] = n
		n++
	}
	for name, i := range map[string]int{"DragEnter": 12, "DragLeave": 13, "DragOver": 14, "Drop": 15} {
		if slot[name] != i {
			t.Fatalf("%s is at slot %d, oledrag calls %d", name, slot[name], i)
		}
	}
}
