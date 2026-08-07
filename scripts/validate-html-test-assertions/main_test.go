// Package main — validate-html-test-assertions CLI.

package main

import (
	"sort"
	"testing"
)

type vioCheck struct {
	File string
	Line int
	Rule string
}

func TestValidateHTMLAssertions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		filename string
		src      string
		want     []vioCheck // empty → clean
	}{
		// ── C2-raw-body ──────────────────────────────────────────────────
		{
			name:     "T1 Body.String inline",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nif strings.Contains(w.Body.String(), \"x\") {}\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 3, Rule: "C2-raw-body"}},
		},
		{
			name:     "T2 Body.String assigned",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nbody := rr.Body.String()\nstrings.Contains(body, \"x\")\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 4, Rule: "C2-raw-body"}},
		},
		{
			name:     "T3 TrimSpace Body.String",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(strings.TrimSpace(w.Body.String()), \"x\")\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 3, Rule: "C2-raw-body"}},
		},
		{
			name:     "T4 menuHTML name heuristic",
			filename: "web-testsuite/menu_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(menuHTML, \"Login\")\n}",
			want:     []vioCheck{{File: "web-testsuite/menu_test.go", Line: 3, Rule: "C2-raw-body"}},
		},
		{
			name:     "T5 error.Error clean",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(err.Error(), \"x\")\n}",
			want:     nil,
		},
		{
			name:     "T6 logs ident clean",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nlogs := \"hi\"\nstrings.Contains(logs, \"x\")\n}",
			want:     nil,
		},
		{
			name:     "T7 Header().Get clean",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(w.Header().Get(\"Content-Type\"), \"x\")\n}",
			want:     nil,
		},

		// ── C3-get-text-content ─────────────────────────────────────────
		{
			name:     "T8 GetTextContent if-init",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nif body := testutil.GetTextContent(n); strings.Contains(body, \"x\") {}\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 3, Rule: "C3-get-text-content"}},
		},
		{
			name:     "T9 GetTextContent assigned",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nbody := testutil.GetTextContent(n)\nstrings.Contains(body, \"x\")\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 4, Rule: "C3-get-text-content"}},
		},

		// ── C3-html-tree ────────────────────────────────────────────────
		{
			name:     "T10 n.Data",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(n.Data, \"x\")\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 3, Rule: "C3-html-tree"}},
		},
		{
			name:     "T11 GetAttr URL allowlist clean",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nclass := testutil.GetAttr(n, \"hx-get\")\nstrings.Contains(class, \"x\")\n}",
			want:     nil,
		},
		{
			name:     "T12 helper-contains",
			filename: "handlers/x_test.go",
			src: `package p

func findTextContains(n *html.Node, text string) bool {
	if strings.Contains(n.Data, text) {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if findTextContains(c, text) {
			return true
		}
	}
	return false
}`,
			want: []vioCheck{
				// C3-helper-contains at func decl name
				{File: "handlers/x_test.go", Line: 3, Rule: "C3-helper-contains"},
				// C3-html-tree at Contains(n.Data, …)
				{File: "handlers/x_test.go", Line: 4, Rule: "C3-html-tree"},
			},
		},
		{
			name:     "T13 bytes.Body.Bytes",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nbytes.Contains(w.Body.Bytes(), \"x\")\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 3, Rule: "C2-raw-body"}},
		},
		{
			name:     "T14 html ident in web-testsuite",
			filename: "web-testsuite/auth_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(html, \"x\")\n}",
			want:     []vioCheck{{File: "web-testsuite/auth_test.go", Line: 3, Rule: "C2-raw-body"}},
		},
		{
			name:     "T15 html ident outside web-testsuite clean",
			filename: "internal/server/foo_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(html, \"x\")\n}",
			want:     nil,
		},
		{
			name:     "T16 template-string in server_templates_test",
			filename: "internal/server/ui/server_templates_test.go",
			src:      "package p\nfunc TestX() {\nbody := buf.String()\nstrings.Contains(body, \"x\")\n}",
			want:     []vioCheck{{File: "internal/server/ui/server_templates_test.go", Line: 4, Rule: "C2-raw-body"}},
		},
		{
			name:     "T17 GetAttr class not allowlisted",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nclass := testutil.GetAttr(n, \"class\")\nstrings.Contains(class, \"x\")\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 4, Rule: "C3-html-tree"}},
		},
		{
			name:     "T18 unknown ident clean",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(text, \"x\")\n}",
			want:     nil,
		},
		{
			name:     "T19 FuncLit on assign RHS",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nf := func(n *html.Node) {\n\tstrings.Contains(n.Data, \"x\")\n}\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 4, Rule: "C3-html-tree"}},
		},
		{
			name:     "T20 IfStmt init before cond",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nif body := w.Body.String(); !strings.Contains(body, \"x\") {}\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 3, Rule: "C2-raw-body"}},
		},
		{
			name:     "T21 readBody in web-testsuite",
			filename: "web-testsuite/menu_test.go",
			src:      "package p\nfunc TestX() {\nbody := readBody(t, resp.Body)\nstrings.Contains(body, \"Login\")\n}",
			want:     []vioCheck{{File: "web-testsuite/menu_test.go", Line: 4, Rule: "C2-raw-body"}},
		},
		{
			name:     "T22 html with err pattern in web-testsuite",
			filename: "web-testsuite/auth_test.go",
			src:      "package p\nfunc TestX() {\nhtml, err := client.FetchDashboard(ctx)\nstrings.Contains(html, \"x\")\n}",
			want:     []vioCheck{{File: "web-testsuite/auth_test.go", Line: 4, Rule: "C2-raw-body"}},
		},
		{
			name:     "T23 FuncLit in t.Run call arg",
			filename: "web-testsuite/menu_test.go",
			src:      "package p\nfunc TestX() {\nt.Run(\"x\", func(t *testing.T) {\n\tbody := readBody(t, r)\n\tstrings.Contains(body, \"Login\")\n})\n}",
			want:     []vioCheck{{File: "web-testsuite/menu_test.go", Line: 5, Rule: "C2-raw-body"}},
		},
		{
			name:     "T24 GetAttr _ with path needle clean",
			filename: "handlers/lightbox_handler_test.go",
			src:      "package p\nfunc TestX() {\ngot := testutil.GetAttr(prev, \"_\")\nstrings.Contains(got, \"/raw-image/3\")\n}",
			want:     nil,
		},
		{
			name:     "T25 GetAttr _ with non-path needle reports",
			filename: "handlers/config_get_test.go",
			src:      "package p\nfunc TestX() {\nhyperscript := testutil.GetAttr(btn, \"_\")\nstrings.Contains(hyperscript, \"install TabSwitcher\")\n}",
			want:     []vioCheck{{File: "handlers/config_get_test.go", Line: 4, Rule: "C3-html-tree"}},
		},
		{
			name:     "T26 RangeStmt value provenance",
			filename: "handlers/x_test.go",
			src:      "package p\nfunc TestX() {\nfor _, body := range menuHTML {\n\tstrings.Contains(body, \"x\")\n}\n}",
			want:     []vioCheck{{File: "handlers/x_test.go", Line: 4, Rule: "C2-raw-body"}},
		},
		{
			name:     "T27 dashboard View assigned",
			filename: "cmd/sfpg-go-dashboard/main_test.go",
			src:      "package p\nfunc TestX() {\nview := m.View()\nstrings.Contains(view, \"Goodbye\")\n}",
			want:     []vioCheck{{File: "cmd/sfpg-go-dashboard/main_test.go", Line: 4, Rule: "C4-tui-view"}},
		},
		{
			name:     "T28 dashboard viewLogin inline",
			filename: "cmd/sfpg-go-dashboard/view_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(m.viewLogin(), \"Login\")\n}",
			want:     []vioCheck{{File: "cmd/sfpg-go-dashboard/view_test.go", Line: 3, Rule: "C4-tui-view"}},
		},
		{
			name:     "T29 dashboard html ident",
			filename: "cmd/sfpg-go-dashboard/parser/dashboard_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(html, \"dashboard-container\")\n}",
			want:     []vioCheck{{File: "cmd/sfpg-go-dashboard/parser/dashboard_test.go", Line: 3, Rule: "C2-raw-body"}},
		},
		{
			name:     "T30 dashboard stderr clean",
			filename: "cmd/sfpg-go-dashboard/main_test.go",
			src:      "package p\nfunc TestX() {\nstrings.Contains(errOut.String(), \"tty unavailable\")\n}",
			want:     nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := analyzeFile(tc.filename, []byte(tc.src))
			if err != nil {
				t.Fatalf("analyzeFile(%q) returned error: %v", tc.filename, err)
			}

			// Sort both by file, line, col for stable comparison.
			sort.Slice(got, func(i, j int) bool {
				if got[i].File != got[j].File {
					return got[i].File < got[j].File
				}
				if got[i].Line != got[j].Line {
					return got[i].Line < got[j].Line
				}
				return got[i].Col < got[j].Col
			})
			sort.Slice(tc.want, func(i, j int) bool {
				if tc.want[i].File != tc.want[j].File {
					return tc.want[i].File < tc.want[j].File
				}
				return tc.want[i].Line < tc.want[j].Line
			})

			if len(got) != len(tc.want) {
				t.Fatalf("got %d violation(s), want %d\ngot:  %+v\nwant: %+v",
					len(got), len(tc.want), got, tc.want)
			}

			for i := range got {
				if got[i].File != tc.want[i].File {
					t.Errorf("violation %d: File = %q, want %q", i, got[i].File, tc.want[i].File)
				}
				if got[i].Line != tc.want[i].Line {
					t.Errorf("violation %d: Line = %d, want %d (Rule=%s)", i, got[i].Line, tc.want[i].Line, got[i].Rule)
				}
				if got[i].Rule != tc.want[i].Rule {
					t.Errorf("violation %d: Rule = %q, want %q (Line=%d)", i, got[i].Rule, tc.want[i].Rule, got[i].Line)
				}
			}
		})
	}
}
