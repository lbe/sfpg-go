// Command validate-html-test-assertions detects forbidden HTML assertion
// patterns in *_test.go files using go/ast.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Violation represents one detected forbidden assertion pattern.
type Violation struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

var ruleMessages = map[string]string{
	"C2-raw-body":           "strings.Contains on response body HTML; use structural assertions",
	"C3-get-text-content":   "strings.Contains on testutil.GetTextContent result; assert structure/exact text instead",
	"C3-html-tree":          "strings.Contains on parsed HTML node/attr content; assert structure instead",
	"C3-helper-contains":    "helper walks html.Node and uses strings.Contains on n.Data; use structural helpers",
	"C4-tui-view":           "strings.Contains on rendered TUI view; use tuiview.AssertPlainIncludes or exact/model assertions",
	"C5-inefficient-parse":  "html.Parse(strings.NewReader(response body)); use testutil.ParseHTML or html.Parse on io.Reader directly",
	"C6-regexp-html":        "regexp on HTML-derived text; use structural or exact assertions",
	"C7-htmx-partial-parse": "FindElementByTag(doc, shell)==nil does not detect HTMX partials on the wire; use testutil.FirstLiteralDocumentShellTag on raw body bytes",
}

func ruleMessage(rule string) string {
	if m, ok := ruleMessages[rule]; ok {
		return m
	}
	return rule
}

// ---------------------------------------------------------------------------
// Exported API
// ---------------------------------------------------------------------------

// analyzeFile parses src and returns violations.
func analyzeFile(filename string, src []byte) ([]Violation, error) {
	filename = filepath.ToSlash(filename)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return nil, err
	}

	helpers := buildHelperReturnKinds(f, filename)
	allowRegexp := buildAllowRegexpLines(f, fset)

	var out []Violation
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		out = append(out, analyzeFuncScope(fset, filename, fd.Body, fd, helpers, allowRegexp)...)
	}

	sortViolations(out)
	return out, nil
}

// ---------------------------------------------------------------------------
// Statement-ordered scope analysis
// ---------------------------------------------------------------------------

func buildAllowRegexpLines(f *ast.File, fset *token.FileSet) map[int]bool {
	allowed := make(map[int]bool)
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if !strings.Contains(c.Text, "validate-html-assertions:allow-regexp") {
				continue
			}
			line := fset.Position(c.End()).Line + 1
			allowed[line] = true
		}
	}
	return allowed
}

func analyzeFuncScope(fset *token.FileSet, filename string, body *ast.BlockStmt, decl *ast.FuncDecl, helpers map[string]string, allowRegexp map[int]bool) []Violation {
	prov := make(map[string]string)
	var out []Violation

	var walkBlock func(block *ast.BlockStmt)
	var handleStmt func(stmt ast.Stmt)
	var walkExpr func(expr ast.Expr)

	walkBlock = func(block *ast.BlockStmt) {
		for _, stmt := range block.List {
			handleStmt(stmt)
		}
	}

	// handleStmt processes one statement in order.
	handleStmt = func(stmt ast.Stmt) {
		switch s := stmt.(type) {
		case *ast.AssignStmt:
			recordAssign(s, prov, filename, helpers)
			for _, rhs := range s.Rhs {
				walkExpr(rhs)
			}

		case *ast.DeclStmt:
			if gen, ok2 := s.Decl.(*ast.GenDecl); ok2 {
				for _, spec := range gen.Specs {
					if vs, ok3 := spec.(*ast.ValueSpec); ok3 {
						recordAssignFromValueSpec(vs, prov, filename, helpers)
						for _, val := range vs.Values {
							walkExpr(val)
						}
					}
				}
			}

		case *ast.IfStmt:
			if s.Init != nil {
				handleStmt(s.Init) // BEFORE Cond
			}
			if s.Cond != nil {
				walkExpr(s.Cond)
			}
			if s.Body != nil {
				walkBlock(s.Body)
			}
			if s.Else != nil {
				switch els := s.Else.(type) {
				case *ast.BlockStmt:
					walkBlock(els)
				case *ast.IfStmt:
					handleStmt(els)
				default:
					// not reached — Else is always BlockStmt or IfStmt
				}
			}

		case *ast.ForStmt:
			if s.Init != nil {
				handleStmt(s.Init)
			}
			if s.Cond != nil {
				walkExpr(s.Cond)
			}
			if s.Body != nil {
				walkBlock(s.Body)
			}
			if s.Post != nil {
				handleStmt(s.Post)
			}

		case *ast.RangeStmt:
			if s.X != nil {
				walkExpr(s.X)
				// Assign provenance from range expression to the iteration variable.
				// Prefer Value; fall back to Key when Value is nil or _.
				target := s.Value
				if target == nil {
					target = s.Key
				} else if id, ok2 := target.(*ast.Ident); ok2 && id.Name == "_" {
					target = s.Key
				}
				if ident, ok2 := target.(*ast.Ident); ok2 && ident.Name != "" && ident.Name != "_" {
					kind := classifyExpr(s.X, prov, filename, helpers)
					if kind != "unknown" {
						prov[ident.Name] = kind
					}
				}
			}
			if s.Body != nil {
				walkBlock(s.Body)
			}

		case *ast.SwitchStmt:
			if s.Init != nil {
				handleStmt(s.Init)
			}
			if s.Tag != nil {
				walkExpr(s.Tag)
			}
			if s.Body != nil {
				walkBlock(s.Body)
			}

		case *ast.TypeSwitchStmt:
			if s.Init != nil {
				handleStmt(s.Init)
			}
			if s.Assign != nil {
				handleStmt(s.Assign)
			}
			if s.Body != nil {
				walkBlock(s.Body)
			}

		case *ast.ExprStmt:
			walkExpr(s.X)

		case *ast.ReturnStmt:
			for _, r := range s.Results {
				walkExpr(r)
			}

		case *ast.DeferStmt:
			walkExpr(s.Call)

		case *ast.GoStmt:
			walkExpr(s.Call)

		case *ast.IncDecStmt:
			walkExpr(s.X)

		case *ast.BlockStmt:
			walkBlock(s)

		case *ast.LabeledStmt:
			handleStmt(s.Stmt)

		case *ast.BranchStmt:
			// break / continue — no expressions to walk.

		case *ast.CommClause:
			// Inside a select/range — walk body.
			for _, cc := range s.Body {
				handleStmt(cc)
			}

		case *ast.CaseClause:
			for _, cc := range s.Body {
				handleStmt(cc)
			}

		default:
			// Fallback: walk any expression nodes.
			ast.Inspect(s, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				checkContainsCall(call, prov, fset, filename, helpers, &out)
				checkHTMLParseCall(call, prov, fset, filename, helpers, &out)
				checkRegexpCall(call, prov, fset, filename, helpers, allowRegexp, &out)
				// Also find FuncLits in call args.
				for _, arg := range call.Args {
					if fl, ok2 := arg.(*ast.FuncLit); ok2 && fl.Body != nil {
						out = append(out, analyzeFuncScope(fset, filename, fl.Body, nil, helpers, allowRegexp)...)
					}
				}
				return true
			})
		}
	}

	// walkExpr walks an expression tree, detecting Contains calls and FuncLits.
	walkExpr = func(expr ast.Expr) {
		ast.Inspect(expr, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncLit:
				if node.Body != nil {
					out = append(out, analyzeFuncScope(fset, filename, node.Body, nil, helpers, allowRegexp)...)
				}
				return false // don't double-walk the FuncLit body
			case *ast.CallExpr:
				checkContainsCall(node, prov, fset, filename, helpers, &out)
				checkHTMLParseCall(node, prov, fset, filename, helpers, &out)
				checkRegexpCall(node, prov, fset, filename, helpers, allowRegexp, &out)
			case *ast.BinaryExpr:
				checkHTMXShellTagNilCompare(node, fset, filename, &out)
			}
			return true
		})
	}

	walkBlock(body)

	// Post-walk: C3-helper-contains detection.
	if decl != nil {
		out = append(out, checkHelperContains(fset, filename, decl)...)
	}

	return out
}

// ---------------------------------------------------------------------------
// Provenance recording
// ---------------------------------------------------------------------------

func recordAssign(stmt *ast.AssignStmt, prov map[string]string, filename string, helpers map[string]string) {
	if len(stmt.Lhs) == 0 || len(stmt.Rhs) == 0 {
		return
	}

	// Single LHS ident → classify RHS.
	if len(stmt.Lhs) >= 1 && len(stmt.Rhs) == 1 {
		lhs, ok := stmt.Lhs[0].(*ast.Ident)
		if !ok {
			return
		}
		// a := b identity chain.
		if rhsIdent, ok2 := stmt.Rhs[0].(*ast.Ident); ok2 {
			if kind, ok3 := prov[rhsIdent.Name]; ok3 {
				prov[lhs.Name] = kind
				return
			}
		}
		kind := classifyExpr(stmt.Rhs[0], prov, filename, helpers)
		if kind != "unknown" {
			prov[lhs.Name] = kind
		}
	}
}

func recordAssignFromValueSpec(spec *ast.ValueSpec, prov map[string]string, filename string, helpers map[string]string) {
	if len(spec.Names) == 1 && len(spec.Values) == 1 {
		kind := classifyExpr(spec.Values[0], prov, filename, helpers)
		if kind != "unknown" {
			prov[spec.Names[0].Name] = kind
		}
	}
}

// ---------------------------------------------------------------------------
// Expression classification
// ---------------------------------------------------------------------------

func isDashboardTestFile(filename string) bool {
	return strings.Contains(filename, "cmd/sfpg-go-dashboard/")
}

func isE2ETestFile(filename string) bool {
	return strings.Contains(filename, "web-testsuite/")
}

func classifyGetAttrArg(attrArg ast.Expr) string {
	lit, ok := attrArg.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "get-attr:unknown"
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "get-attr:unknown"
	}
	return "get-attr:" + val
}

// buildHelperReturnKinds maps test helper func names to return provenance (e.g. get-text-content).
func buildHelperReturnKinds(f *ast.File, filename string) map[string]string {
	kinds := make(map[string]string)
	var candidates []*ast.FuncDecl
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || !funcDeclReturnsString(fd) {
			continue
		}
		if testingTParamName(fd) == "" || !funcDeclCallsTHelper(fd) {
			continue
		}
		candidates = append(candidates, fd)
	}

	for {
		changed := false
		for _, fd := range candidates {
			name := fd.Name.Name
			if kinds[name] != "" {
				continue
			}
			if helperBodyReturnsGetTextContent(fd, kinds, filename) {
				kinds[name] = "get-text-content"
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return kinds
}

func funcDeclReturnsString(fd *ast.FuncDecl) bool {
	if fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
		return false
	}
	typ := fd.Type.Results.List[0].Type
	ident, ok := typ.(*ast.Ident)
	return ok && ident.Name == "string"
}

func testingTParamName(fd *ast.FuncDecl) string {
	if fd.Type.Params == nil || len(fd.Type.Params.List) == 0 {
		return ""
	}
	field := fd.Type.Params.List[0]
	if !isStarSelectorExpr(field.Type, "testing", "T") {
		return ""
	}
	if len(field.Names) == 0 {
		return ""
	}
	return field.Names[0].Name
}

func funcDeclCallsTHelper(fd *ast.FuncDecl) bool {
	tName := testingTParamName(fd)
	if tName == "" {
		return false
	}
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if ok && id.Name == tName && sel.Sel.Name == "Helper" {
			found = true
			return false
		}
		return true
	})
	return found
}

func helperBodyReturnsGetTextContent(fd *ast.FuncDecl, helpers map[string]string, filename string) bool {
	hasReturn := false
	ok := true
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		rs, isRet := n.(*ast.ReturnStmt)
		if !isRet {
			return true
		}
		for _, r := range rs.Results {
			hasReturn = true
			if classifyHelperReturnExpr(r, helpers, filename) != "get-text-content" {
				ok = false
				return false
			}
		}
		return true
	})
	return hasReturn && ok
}

func classifyHelperReturnExpr(expr ast.Expr, helpers map[string]string, filename string) string {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return classifyHelperReturnExpr(e.X, helpers, filename)
	case *ast.CallExpr:
		switch fun := e.Fun.(type) {
		case *ast.Ident:
			if kind, ok := helpers[fun.Name]; ok && kind != "" {
				return kind
			}
			if isE2ETestFile(filename) && fun.Name == "GetTextContent" {
				return "get-text-content"
			}
		case *ast.SelectorExpr:
			if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "testutil" && fun.Sel.Name == "GetTextContent" {
				return "get-text-content"
			}
		}
	}
	return "unknown"
}

func classifyExpr(expr ast.Expr, prov map[string]string, filename string, helpers map[string]string) string {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return classifyCallExpr(e, prov, filename, helpers)

	case *ast.SelectorExpr:
		// *.Data → html-data
		if e.Sel.Name == "Data" {
			return "html-data"
		}
		return "unknown"

	case *ast.Ident:
		return classifyIdent(e, prov, filename, helpers)

	case *ast.ParenExpr:
		return classifyExpr(e.X, prov, filename, helpers)

	default:
		return "unknown"
	}
}

func classifyCallExpr(call *ast.CallExpr, prov map[string]string, filename string, helpers map[string]string) string {
	// Rule 1: strings.TrimSpace(x) / strings.ToLower(x) → unwrap.
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if pkg, ok2 := sel.X.(*ast.Ident); ok2 && pkg.Name == "strings" {
			if (sel.Sel.Name == "TrimSpace" || sel.Sel.Name == "ToLower") && len(call.Args) == 1 {
				return classifyExpr(call.Args[0], prov, filename, helpers)
			}
		}
	}

	// Examine the call's Fun more closely.
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		// Rule 2: *.Body.String / *.Body.Bytes → body-string.
		if fun.Sel.Name == "String" || fun.Sel.Name == "Bytes" {
			if inner, ok2 := fun.X.(*ast.SelectorExpr); ok2 && inner.Sel.Name == "Body" {
				return "body-string"
			}
		}

		// Rule 4: testutil.GetTextContent → get-text-content.
		if pkg, ok2 := fun.X.(*ast.Ident); ok2 && pkg.Name == "testutil" && fun.Sel.Name == "GetTextContent" {
			return "get-text-content"
		}

		// Rule 5: testutil.GetAttr → get-attr:NAME.
		if pkg, ok2 := fun.X.(*ast.Ident); ok2 && pkg.Name == "testutil" && fun.Sel.Name == "GetAttr" && len(call.Args) >= 2 {
			return classifyGetAttrArg(call.Args[1])
		}

		// Rule 7: *.Error → error (err.Error, w.Body.Error, etc.).
		if fun.Sel.Name == "Error" {
			return "error"
		}

		// Rule 8: *.Header().Get(...) → header.
		if fun.Sel.Name == "Get" {
			if innerCall, ok2 := fun.X.(*ast.CallExpr); ok2 {
				if innerSel, ok3 := innerCall.Fun.(*ast.SelectorExpr); ok3 && innerSel.Sel.Name == "Header" {
					return "header"
				}
			}
		}

		// Rule 9: *.String in server_templates_test.go → template-string.
		if fun.Sel.Name == "String" && strings.HasSuffix(filename, "internal/server/ui/server_templates_test.go") {
			return "template-string"
		}

		// Rule 3 (selector): .readBody(...) → body-string.
		if fun.Sel.Name == "readBody" {
			return "body-string"
		}

		// Rule 10: Bubble Tea rendered views in sfpg-go-dashboard tests.
		if isDashboardTestFile(filename) && len(call.Args) == 0 {
			switch fun.Sel.Name {
			case "View", "viewLogin", "viewDashboard":
				return "tui-view"
			}
		}

	case *ast.Ident:
		// Rule 3: readBody(...) → body-string.
		if fun.Name == "readBody" {
			return "body-string"
		}

		// Rule 6: scriptText(...) → script-text.
		if fun.Name == "scriptText" {
			return "script-text"
		}

		// string(bodyBytes) when bodyBytes is a response body → body-string.
		if fun.Name == "string" && len(call.Args) == 1 {
			if classifyExpr(call.Args[0], prov, filename, helpers) == "body-string" {
				return "body-string"
			}
		}

		// P1: test helper returning aggregated GetTextContent.
		if helpers != nil {
			if kind, ok := helpers[fun.Name]; ok && kind != "" {
				return kind
			}
		}

		// P1: web-testsuite package-local HTML helpers (same as testutil.*).
		if isE2ETestFile(filename) {
			switch fun.Name {
			case "GetTextContent":
				return "get-text-content"
			case "GetAttr":
				if len(call.Args) >= 2 {
					return classifyGetAttrArg(call.Args[1])
				}
			}
		}
	}

	return "unknown"
}

func classifyIdent(ident *ast.Ident, prov map[string]string, filename string, helpers map[string]string) string {
	name := ident.Name

	// Name heuristics BEFORE prov lookup.
	switch name {
	case "menuHTML", "menuHTML2", "menuHTML3":
		return "menu-html"
	case "view":
		if isDashboardTestFile(filename) {
			return "tui-view"
		}
	case "html":
		if strings.Contains(filename, "web-testsuite/") || isDashboardTestFile(filename) {
			return "menu-html"
		}
	case "logs", "logBuf":
		if _, ok := prov[name]; !ok {
			return "log"
		}
	}

	// Prov lookup.
	if kind, ok := prov[name]; ok {
		return kind
	}

	return "unknown"
}

// ---------------------------------------------------------------------------
// Contains-like detection
// ---------------------------------------------------------------------------

func isContainsLike(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return (pkg.Name == "strings" || pkg.Name == "bytes") && sel.Sel.Name == "Contains"
}

func checkContainsCall(call *ast.CallExpr, prov map[string]string, fset *token.FileSet, filename string, helpers map[string]string, out *[]Violation) {
	if !isContainsLike(call) || len(call.Args) == 0 {
		return
	}

	// Path-needle exception: get-attr + needle literal starting with "/" → silent.
	if len(call.Args) >= 2 && isPathNeedleException(call.Args[0], call.Args[1], prov, filename, helpers) {
		return
	}

	kind := classifyExpr(call.Args[0], prov, filename, helpers)
	if rule := ruleFor(kind); rule != "" {
		pos := fset.Position(call.Pos())
		*out = append(*out, Violation{
			File:    filename,
			Line:    pos.Line,
			Col:     pos.Column,
			Rule:    rule,
			Message: ruleMessage(rule),
		})
	}
}

func regexpHaystackArg(call *ast.CallExpr) (ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	switch sel.Sel.Name {
	case "MatchString":
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "regexp" {
			if len(call.Args) < 2 {
				return nil, false
			}
			return call.Args[1], true
		}
		if len(call.Args) < 1 {
			return nil, false
		}
		return call.Args[0], true
	case "FindStringSubmatch", "FindString", "FindAllString":
		if len(call.Args) < 1 {
			return nil, false
		}
		return call.Args[0], true
	default:
		return nil, false
	}
}

func isRegexpHTMLCall(call *ast.CallExpr) bool {
	_, ok := regexpHaystackArg(call)
	return ok
}

func checkRegexpCall(call *ast.CallExpr, prov map[string]string, fset *token.FileSet, filename string, helpers map[string]string, allowRegexp map[int]bool, out *[]Violation) {
	if !isRegexpHTMLCall(call) {
		return
	}
	haystack, ok := regexpHaystackArg(call)
	if !ok {
		return
	}

	kind := classifyExpr(haystack, prov, filename, helpers)
	if rule := ruleForRegexp(kind); rule != "" {
		pos := fset.Position(call.Pos())
		if allowRegexp != nil && allowRegexp[pos.Line] {
			return
		}
		*out = append(*out, Violation{
			File:    filename,
			Line:    pos.Line,
			Col:     pos.Column,
			Rule:    rule,
			Message: ruleMessage(rule),
		})
	}
}

func ruleForRegexp(kind string) string {
	if ruleFor(kind) != "" {
		return "C6-regexp-html"
	}
	return ""
}

func isHTMLParseCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "html" && sel.Sel.Name == "Parse"
}

func isInefficientHTMLParseArg(arg ast.Expr, prov map[string]string, filename string, helpers map[string]string) bool {
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "strings" || sel.Sel.Name != "NewReader" || len(call.Args) != 1 {
		return false
	}
	return classifyExpr(call.Args[0], prov, filename, helpers) == "body-string"
}

func checkHTMLParseCall(call *ast.CallExpr, prov map[string]string, fset *token.FileSet, filename string, helpers map[string]string, out *[]Violation) {
	if !isHTMLParseCall(call) || len(call.Args) == 0 {
		return
	}
	if !isInefficientHTMLParseArg(call.Args[0], prov, filename, helpers) {
		return
	}
	pos := fset.Position(call.Pos())
	*out = append(*out, Violation{
		File:    filename,
		Line:    pos.Line,
		Col:     pos.Column,
		Rule:    "C5-inefficient-parse",
		Message: ruleMessage("C5-inefficient-parse"),
	})
}

func isNilExpr(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

func shellTagFromFindElementByTagCall(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "FindElementByTag" || len(call.Args) < 2 {
		return ""
	}
	lit, ok := call.Args[1].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	switch val {
	case "html", "head", "body":
		return val
	default:
		return ""
	}
}

// checkHTMXShellTagNilCompare flags FindElementByTag(..., "html"|"head"|"body") == nil.
// html.Parse synthesizes those nodes for fragments; use FirstLiteralDocumentShellTag on wire bytes.
func checkHTMXShellTagNilCompare(expr ast.Expr, fset *token.FileSet, filename string, out *[]Violation) {
	be, ok := expr.(*ast.BinaryExpr)
	if !ok || be.Op != token.EQL {
		return
	}
	var call *ast.CallExpr
	switch {
	case isNilExpr(be.Y):
		call, _ = be.X.(*ast.CallExpr)
	case isNilExpr(be.X):
		call, _ = be.Y.(*ast.CallExpr)
	default:
		return
	}
	if call == nil {
		return
	}
	if shellTagFromFindElementByTagCall(call) == "" {
		return
	}
	pos := fset.Position(be.Pos())
	*out = append(*out, Violation{
		File:    filename,
		Line:    pos.Line,
		Col:     pos.Column,
		Rule:    "C7-htmx-partial-parse",
		Message: ruleMessage("C7-htmx-partial-parse"),
	})
}

// isPathNeedleException returns true when the haystack classifies as get-attr:*
// (any attribute, including "_") and the needle is a string literal whose
// decoded value starts with "/" (URL/path assertion, not HTML body text).
func isPathNeedleException(haystack, needle ast.Expr, prov map[string]string, filename string, helpers map[string]string) bool {
	kind := classifyExpr(haystack, prov, filename, helpers)
	if !strings.HasPrefix(kind, "get-attr:") {
		return false
	}
	lit, ok := needle.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}
	return strings.HasPrefix(val, "/")
}

// ---------------------------------------------------------------------------
// Rule mapping
// ---------------------------------------------------------------------------

func ruleFor(kind string) string {
	switch kind {
	case "tui-view":
		return "C4-tui-view"
	case "body-string", "template-string", "menu-html":
		return "C2-raw-body"
	case "get-text-content":
		return "C3-get-text-content"
	case "html-data", "script-text":
		return "C3-html-tree"
	case "get-attr:unknown":
		return "C3-html-tree"
	default:
		if strings.HasPrefix(kind, "get-attr:") {
			return "C3-html-tree"
		}
		return ""
	}
}

// ---------------------------------------------------------------------------
// C3-helper-contains detection
// ---------------------------------------------------------------------------

func checkHelperContains(fset *token.FileSet, filename string, decl *ast.FuncDecl) []Violation {
	if decl.Body == nil || decl.Type.Params == nil {
		return nil
	}

	// Check for *html.Node parameter.
	hasHTMLNode := false
	for _, field := range decl.Type.Params.List {
		if isStarSelectorExpr(field.Type, "html", "Node") {
			hasHTMLNode = true
			break
		}
	}
	if !hasHTMLNode {
		return nil
	}

	hasContainsData := false
	hasRecursionOrWalk := false

	ast.Inspect(decl.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if isContainsLike(node) && len(node.Args) > 0 {
				if sel, ok := node.Args[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "Data" {
					hasContainsData = true
				}
			}
			// Recursive call: function name matches.
			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == decl.Name.Name {
				hasRecursionOrWalk = true
			}
		case *ast.SelectorExpr:
			if node.Sel.Name == "FirstChild" || node.Sel.Name == "NextSibling" {
				hasRecursionOrWalk = true
			}
		}
		return true
	})

	if hasContainsData && hasRecursionOrWalk {
		pos := fset.Position(decl.Name.NamePos)
		return []Violation{{
			File:    filename,
			Line:    pos.Line,
			Col:     pos.Column,
			Rule:    "C3-helper-contains",
			Message: ruleMessage("C3-helper-contains"),
		}}
	}
	return nil
}

func isStarSelectorExpr(expr ast.Expr, pkg, name string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkgIdent.Name == pkg && sel.Sel.Name == name
}

// ---------------------------------------------------------------------------
// Sorting
// ---------------------------------------------------------------------------

func sortViolations(v []Violation) {
	sort.Slice(v, func(i, j int) bool {
		if v[i].File != v[j].File {
			return v[i].File < v[j].File
		}
		if v[i].Line != v[j].Line {
			return v[i].Line < v[j].Line
		}
		return v[i].Col < v[j].Col
	})
}

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

func main() {
	staged := flag.Bool("staged", false, "Only staged *_test.go lines (git diff --cached)")
	jsonOut := flag.Bool("json", false, "JSON output only")
	quiet := flag.Bool("quiet", false, "Violation lines only")
	flag.Parse()

	var paths []string
	if flag.NArg() == 0 {
		paths = []string{"."}
	} else {
		paths = flag.Args()
	}

	if *staged {
		os.Exit(runStaged(*jsonOut, *quiet))
	}

	files := collectFiles(paths)

	var allViolations []Violation

	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", f, err)
			os.Exit(2)
		}
		vios, err := analyzeFile(f, src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", f, err)
			os.Exit(2)
		}
		allViolations = append(allViolations, vios...)
	}

	sortViolations(allViolations)

	// Count files with \u22651 violation for the summary.
	violationFiles := make(map[string]bool)
	for _, v := range allViolations {
		violationFiles[v.File] = true
	}
	violationFileCount := len(violationFiles)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(allViolations); err != nil {
			fmt.Fprintf(os.Stderr, "json encode error: %v\n", err)
			os.Exit(2)
		}
		if len(allViolations) > 0 {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if *quiet {
		for _, v := range allViolations {
			fmt.Printf("%s:%d:%d: %s: %s\n", v.File, v.Line, v.Col, v.Rule, v.Message)
		}
	} else {
		for _, v := range allViolations {
			fmt.Printf("%s:%d:%d: %s: %s\n", v.File, v.Line, v.Col, v.Rule, v.Message)
		}
		if len(allViolations) > 0 {
			fmt.Printf("\nvalidate-html-test-assertions: %d violation(s) in %d file(s)\n",
				len(allViolations), violationFileCount)
		} else {
			fmt.Printf("validate-html-test-assertions: 0 violation(s) in %d file(s)\n", violationFileCount)
		}
	}

	if len(allViolations) > 0 {
		os.Exit(1)
	}
	os.Exit(0)
}

// ---------------------------------------------------------------------------
// File collection
// ---------------------------------------------------------------------------

func collectFiles(paths []string) []string {
	skipDirs := map[string]bool{
		"vendor":       true,
		"node_modules": true,
		".git":         true,
		"tmp":          true,
		"zarchive":     true,
		"scripts":      true,
		".worktrees":   true,
	}

	var files []string
	for _, root := range paths {
		if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() && skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			if !info.IsDir() && strings.HasSuffix(info.Name(), "_test.go") {
				files = append(files, path)
			}
			return nil
		}); err != nil {
			fmt.Fprintf(os.Stderr, "error walking %s: %v\n", root, err)
			os.Exit(2)
		}
	}
	return files
}

// ---------------------------------------------------------------------------
// -staged mode
// ---------------------------------------------------------------------------

func runStaged(jsonOut, quiet bool) int {
	cmd := exec.Command("git", "diff", "--cached", "-U0", "--", "*.go")
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git diff --cached failed: %v\n", err)
		return 2
	}

	// Parse diff into staged lines per file.
	type stagedInfo struct {
		file  string
		lines map[int]bool // line numbers that changed
	}
	var staged []stagedInfo
	var currentFile string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "+++ b/"); ok {
			currentFile = filepath.ToSlash(after)
			if !strings.HasSuffix(currentFile, "_test.go") {
				currentFile = ""
			}
			continue
		}
		if currentFile == "" {
			continue
		}
		if strings.HasPrefix(line, "@@") {
			// @@ -a,b +c,d @@
			parts := strings.Split(line, " ")
			if len(parts) < 3 {
				continue
			}
			newPart := parts[2] // +c,d or +c
			newPart = strings.TrimPrefix(newPart, "+")
			var start, count int
			if _, err := fmt.Sscanf(newPart, "%d,%d", &start, &count); err != nil {
				if _, err := fmt.Sscanf(newPart, "%d", &start); err != nil {
					continue
				}
				count = 1
			}
			si := stagedInfo{file: currentFile, lines: make(map[int]bool)}
			for l := start; l < start+count; l++ {
				si.lines[l] = true
			}
			staged = append(staged, si)
		}
	}

	if len(staged) == 0 {
		return 0
	}

	// Collect unique files.
	fileSet := make(map[string]bool)
	for _, si := range staged {
		fileSet[si.file] = true
	}

	var allViolations []Violation
	for f := range fileSet {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", f, err)
			return 2
		}
		vios, err := analyzeFile(f, src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", f, err)
			return 2
		}

		// Build staged-line set for this file.
		fileStaged := make(map[int]bool)
		for _, si := range staged {
			if si.file == f {
				for l := range si.lines {
					fileStaged[l] = true
				}
			}
		}

		for _, v := range vios {
			if fileStaged[v.Line] {
				allViolations = append(allViolations, v)
			}
		}
	}

	sortViolations(allViolations)

	stagedViolationFiles := make(map[string]bool)
	for _, v := range allViolations {
		stagedViolationFiles[v.File] = true
	}
	stagedViolationCount := len(stagedViolationFiles)

	switch {
	case jsonOut:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(allViolations); err != nil {
			fmt.Fprintf(os.Stderr, "json encode error: %v\n", err)
			return 2
		}

	case quiet:
		for _, v := range allViolations {
			fmt.Printf("%s:%d:%d: %s: %s\n", v.File, v.Line, v.Col, v.Rule, v.Message)
		}

	default:
		for _, v := range allViolations {
			fmt.Printf("%s:%d:%d: %s: %s\n", v.File, v.Line, v.Col, v.Rule, v.Message)
		}
		if len(allViolations) > 0 {
			fmt.Printf("\nvalidate-html-test-assertions: %d violation(s) in %d file(s)\n",
				len(allViolations), stagedViolationCount)
		} else {
			fmt.Printf("validate-html-test-assertions: 0 violation(s) in %d file(s)\n", stagedViolationCount)
		}
	}

	if len(allViolations) > 0 {
		return 1
	}
	return 0
}
