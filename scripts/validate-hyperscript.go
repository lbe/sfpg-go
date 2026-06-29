// cmd/validate-hyperscript/main.go
//
// Validates hyperscript syntax in HTML/Go template files using the
// hyperscript parser running in goja (pure Go JavaScript runtime).
//
// Usage:
//
//	validate-hyperscript [flags] <file1.html> [dir1] [file2.html.tmpl ...]
//
// Flags:
//
//	-json          Output raw JSON instead of human-readable format
//	-hyperscript   Path to local hyperscript.js (default: fetch from CDN)
//	-quiet         Only output errors, not valid results
//	-ext           File extensions to process (default: .html,.tmpl,.gohtml)
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dop251/goja"
)

const hyperscriptCDN = "https://unpkg.com/hyperscript.org@0.9.12/dist/_hyperscript.min.js"

// HyperscriptSource represents extracted hyperscript code with its location.
type HyperscriptSource struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	SourceType string `json:"source_type"` // "attribute" or "script_block"
	Code       string `json:"code"`
}

// ValidationResult holds the result of validating a single hyperscript snippet.
type ValidationResult struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	SourceType string `json:"source_type"`
	Code       string `json:"code"`
	Valid      bool   `json:"valid"`
	Error      string `json:"error,omitempty"`
}

// ValidationReport is the complete output of the validation run.
type ValidationReport struct {
	TotalFiles   int                `json:"total_files"`
	TotalScripts int                `json:"total_scripts"`
	ValidCount   int                `json:"valid_count"`
	InvalidCount int                `json:"invalid_count"`
	Results      []ValidationResult `json:"results"`
	FwdRefCount  int                `json:"fwd_ref_count"`
	FwdRefIssues []ForwardRefIssue  `json:"fwd_ref_issues,omitempty"`
}

func main() {
	jsonOutput := flag.Bool("json", false, "Output raw JSON (default is formatted for readability)")
	hsPath := flag.String("hyperscript", "", "Path to local hyperscript.js (default: fetch from CDN)")
	quiet := flag.Bool("quiet", false, "Only output errors, not valid results")
	extFlag := flag.String("ext", ".html,.tmpl,.gohtml", "Comma-separated file extensions to process")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: validate-hyperscript [flags] <file1.html> [dir1] [file2.html.tmpl ...]")
		fmt.Fprintln(os.Stderr, "\nArguments can be files or directories. Directories are processed recursively.")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Parse extensions
	extensions := parseExtensions(*extFlag)

	// Expand arguments to file list (handles directories recursively)
	files, err := expandArgs(args, extensions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error expanding arguments: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "No matching files found")
		os.Exit(1)
	}

	// Load hyperscript library
	hsCode, err := loadHyperscript(*hsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading hyperscript: %v\n", err)
		os.Exit(1)
	}

	// Initialize goja VM with hyperscript
	vm, err := initVM(hsCode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing JS VM: %v\n", err)
		os.Exit(1)
	}

	// Process all files
	report := ValidationReport{
		Results: []ValidationResult{},
	}

	for _, file := range files {
		sources, err := extractHyperscript(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", file, err)
			continue
		}
		report.TotalFiles++

		var fileSources []HyperscriptSource
		for _, src := range sources {
			result := validateHyperscript(vm, src)
			report.Results = append(report.Results, result)
			fileSources = append(fileSources, src)
			report.TotalScripts++
			if result.Valid {
				report.ValidCount++
			} else {
				report.InvalidCount++
			}
		}

		// Check for Go template escaping issues in script blocks
		escapeIssues := checkGoTemplateEscaping(fileSources)
		if len(escapeIssues) > 0 {
			report.FwdRefIssues = append(report.FwdRefIssues, escapeIssues...)
			report.FwdRefCount += len(escapeIssues)
		}

		// Check for forward references across all snippets in this file
		issues := checkFileForwardReferences(vm, fileSources)
		if len(issues) > 0 {
			report.FwdRefIssues = append(report.FwdRefIssues, issues...)
			for _, issue := range issues {
				if issue.Type == "error" {
					report.InvalidCount++
				} else {
					report.FwdRefCount++
				}
			}
		}
	}

	// Output results
	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "error encoding JSON: %v\n", err)
			os.Exit(1)
		}
	} else {
		printHumanReadable(report, *quiet)
	}

	// Exit with error code if any invalid
	if report.InvalidCount > 0 {
		os.Exit(1)
	}
}

// parseExtensions splits the comma-separated extension string into a map for fast lookup.
func parseExtensions(extFlag string) map[string]bool {
	extensions := make(map[string]bool)
	for ext := range strings.SplitSeq(extFlag, ",") {
		ext = strings.TrimSpace(ext)
		if ext != "" {
			if !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			extensions[ext] = true
		}
	}
	return extensions
}

// expandArgs takes a list of files/directories and returns all matching files.
// Directories are walked recursively, filtering by extension.
func expandArgs(args []string, extensions map[string]bool) ([]string, error) {
	var files []string
	seen := make(map[string]bool) // Deduplicate

	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("cannot access %s: %w", arg, err)
		}

		if info.IsDir() {
			// Walk directory recursively
			err := filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}

				// Skip hidden directories
				if d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != arg {
					return filepath.SkipDir
				}

				if !d.IsDir() && hasMatchingExtension(path, extensions) {
					absPath, err := filepath.Abs(path)
					if err != nil {
						return err
					}
					if !seen[absPath] {
						seen[absPath] = true
						files = append(files, path)
					}
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("walking directory %s: %w", arg, err)
			}
		} else {
			// Single file - add if extension matches (or add anyway if explicitly specified)
			absPath, err := filepath.Abs(arg)
			if err != nil {
				return nil, err
			}
			if !seen[absPath] {
				seen[absPath] = true
				files = append(files, arg)
			}
		}
	}

	return files, nil
}

// hasMatchingExtension checks if the file path ends with any of the target extensions.
// Handles compound extensions like .html.tmpl
func hasMatchingExtension(path string, extensions map[string]bool) bool {
	name := filepath.Base(path)

	// Check compound extensions first (e.g., .html.tmpl)
	for ext := range extensions {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}

	// Check simple extension
	ext := filepath.Ext(path)
	return extensions[ext]
}

// loadHyperscript fetches the hyperscript library from CDN or local file.
func loadHyperscript(localPath string) (string, error) {
	if localPath != "" {
		data, err := os.ReadFile(localPath)
		if err != nil {
			return "", fmt.Errorf("reading local file: %w", err)
		}
		return string(data), nil
	}

	resp, err := http.Get(hyperscriptCDN)
	if err != nil {
		return "", fmt.Errorf("fetching from CDN: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("CDN returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	return string(data), nil
}

// initVM creates a goja VM with DOM stubs and loads hyperscript.
func initVM(hsCode string) (*goja.Runtime, error) {
	vm := goja.New()

	// Minimal DOM stubs required for hyperscript parser.
	domStubs := `
		var console = {
			log: function() {},
			warn: function() {},
			error: function() {},
			info: function() {},
			debug: function() {}
		};

		var window = {
			console: console,
			setTimeout: function(fn, ms) { fn(); return 1; },
			clearTimeout: function() {},
			setInterval: function(fn, ms) { return 1; },
			clearInterval: function() {},
			requestAnimationFrame: function(fn) { return 1; },
			cancelAnimationFrame: function() {},
			location: { href: '', protocol: 'https:', host: 'localhost' },
			navigator: { userAgent: 'goja' },
			getComputedStyle: function() { return {}; },
			customElements: { define: function() {}, get: function() {} }
		};

		var self = window;
		var globalThis = window;

		var document = {
			body: { 
				addEventListener: function() {},
				removeEventListener: function() {},
				appendChild: function() {},
				style: {}
			},
			head: { appendChild: function() {} },
			documentElement: { style: {} },
			createElement: function(tag) {
				return {
					tagName: tag.toUpperCase(),
					style: {},
					setAttribute: function() {},
					getAttribute: function() { return null; },
					addEventListener: function() {},
					removeEventListener: function() {},
					appendChild: function() {},
					classList: {
						add: function() {},
						remove: function() {},
						toggle: function() {},
						contains: function() { return false; }
					}
				};
			},
			createTextNode: function(text) { return { nodeValue: text }; },
			querySelector: function() { return null; },
			querySelectorAll: function() { return []; },
			getElementById: function() { return null; },
			getElementsByClassName: function() { return []; },
			getElementsByTagName: function() { return []; },
			addEventListener: function() {},
			removeEventListener: function() {},
			readyState: 'complete',
			currentScript: null
		};

		function Element() {}
		Element.prototype = {
			addEventListener: function() {},
			removeEventListener: function() {},
			setAttribute: function() {},
			getAttribute: function() { return null; },
			appendChild: function() {},
			style: {},
			classList: {
				add: function() {},
				remove: function() {},
				toggle: function() {},
				contains: function() { return false; }
			}
		};

		function Node() {}
		Node.prototype = {};
		Node.ELEMENT_NODE = 1;
		Node.TEXT_NODE = 3;

		function HTMLElement() {}
		HTMLElement.prototype = Object.create(Element.prototype);

		function Event(type) { this.type = type; }
		function CustomEvent(type, opts) { 
			this.type = type; 
			this.detail = opts ? opts.detail : null; 
		}

		function MutationObserver(callback) { this.callback = callback; }
		MutationObserver.prototype = {
			observe: function() {},
			disconnect: function() {}
		};

		function NodeList() {}
		function HTMLCollection() {}
		function DOMTokenList() {}
	`

	if _, err := vm.RunString(domStubs); err != nil {
		return nil, fmt.Errorf("setting up DOM stubs: %w", err)
	}

	if _, err := vm.RunString(hsCode); err != nil {
		return nil, fmt.Errorf("loading hyperscript: %w", err)
	}

	// Validation helper that uses window._hyperscript
	validatorCode := `
		function __validateHyperscript(code) {
			try {
				var hs = window._hyperscript || _hyperscript;
				if (!hs) {
					return { valid: false, error: '_hyperscript not loaded' };
				}
				hs.parse(code);

				// Validate fetch command conversion types
				// The fetch command in Hyperscript only supports specific response type conversions
				var fetchAsPattern = /fetch\s+(.+?)\s+as\s+(\w+)/g;
				var match;
				var validFetchTypes = ['text', 'json', 'arraybuffer', 'blob', 'response'];

				while ((match = fetchAsPattern.exec(code)) !== null) {
					var conversionType = match[2];
					if (!validFetchTypes.includes(conversionType)) {
						return {
							valid: false,
							error: 'Invalid fetch conversion type: "' + conversionType + '". ' +
							       'Valid types for fetch: ' + validFetchTypes.join(', ') + '. ' +
							       'Tip: For HTML content, use "put result into" without the "as" clause.'
						};
					}
				}

				return { valid: true, error: '' };
			} catch (e) {
				return { valid: false, error: e.message || String(e) };
			}
		}
	`

	if _, err := vm.RunString(validatorCode); err != nil {
		return nil, fmt.Errorf("setting up validator: %w", err)
	}

	// Feature extraction function: returns JSON array of {keyword, name} for behavior/install features
	featureExtractorCode := `
		function __extractHyperscriptFeatures(code) {
			try {
				var hs = window._hyperscript || _hyperscript;
				if (!hs) {
					return '[]';
				}
				var result = hs.parse(code);
				var features = [];
				if (result && result.features) {
					for (var i = 0; i < result.features.length; i++) {
						var f = result.features[i];
						if (f.keyword === 'behavior' || f.keyword === 'install') {
							var src = f.programSource || '';
							var name = '';
							var idx = src.indexOf(f.keyword);
							if (idx >= 0) {
								var afterKeyword = src.substring(idx + f.keyword.length).trim();
								var spaceIdx = afterKeyword.indexOf(' ');
								if (spaceIdx > 0) {
									name = afterKeyword.substring(0, spaceIdx);
								} else {
									name = afterKeyword;
								}
							}
							features.push({keyword: f.keyword, name: name});
						}
					}
				}
				return JSON.stringify(features);
			} catch (e) {
				return '[]';
			}
		}
	`

	if _, err := vm.RunString(featureExtractorCode); err != nil {
		return nil, fmt.Errorf("setting up feature extractor: %w", err)
	}

	return vm, nil
}

// extractHyperscript finds all hyperscript code in an HTML file.
func extractHyperscript(filename string) ([]HyperscriptSource, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var sources []HyperscriptSource
	scanner := bufio.NewScanner(file)
	lineNum := 0

	// Patterns for script blocks
	scriptStartPattern := regexp.MustCompile(`<script\s+type\s*=\s*["']text/hyperscript["'][^>]*>`)
	scriptEndPattern := regexp.MustCompile(`</script>`)

	// Patterns for single-line attributes (must have closing quote on same line)
	attrPatternDQ := regexp.MustCompile(`_="([^"]*)"`)
	attrPatternSQ := regexp.MustCompile(`_='([^']*)'`)

	// State tracking
	inScriptBlock := false
	scriptBlockStart := 0
	var scriptBlockContent strings.Builder

	inMultiLineAttr := false
	multiLineAttrStart := 0
	var multiLineAttrContent strings.Builder
	multiLineAttrQuote := byte(0) // " or '

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Handle script blocks
		if !inScriptBlock && !inMultiLineAttr {
			if scriptStartPattern.MatchString(line) {
				inScriptBlock = true
				scriptBlockStart = lineNum

				parts := scriptStartPattern.Split(line, 2)
				if len(parts) > 1 {
					remainder := parts[1]
					if scriptEndPattern.MatchString(remainder) {
						content := scriptEndPattern.Split(remainder, 2)[0]
						if strings.TrimSpace(content) != "" {
							sources = append(sources, HyperscriptSource{
								File:       filename,
								Line:       lineNum,
								SourceType: "script_block",
								Code:       strings.TrimSpace(content),
							})
						}
						inScriptBlock = false
					} else {
						scriptBlockContent.WriteString(remainder)
						scriptBlockContent.WriteString("\n")
					}
				}
				continue
			}
		}

		if inScriptBlock {
			if scriptEndPattern.MatchString(line) {
				content := scriptEndPattern.Split(line, 2)[0]
				scriptBlockContent.WriteString(content)

				finalContent := strings.TrimSpace(scriptBlockContent.String())
				if finalContent != "" {
					sources = append(sources, HyperscriptSource{
						File:       filename,
						Line:       scriptBlockStart,
						SourceType: "script_block",
						Code:       finalContent,
					})
				}

				inScriptBlock = false
				scriptBlockContent.Reset()
				continue
			}
			scriptBlockContent.WriteString(line)
			scriptBlockContent.WriteString("\n")
			continue
		}

		// Handle multi-line attributes
		if inMultiLineAttr {
			// Look for closing quote
			closingIdx := findClosingQuote(line, multiLineAttrQuote)
			if closingIdx >= 0 {
				// Found closing quote
				multiLineAttrContent.WriteString(line[:closingIdx])

				finalContent := strings.TrimSpace(multiLineAttrContent.String())
				if finalContent != "" {
					code := decodeHTMLEntities(finalContent)
					sources = append(sources, HyperscriptSource{
						File:       filename,
						Line:       multiLineAttrStart,
						SourceType: "attribute",
						Code:       code,
					})
				}

				inMultiLineAttr = false
				multiLineAttrContent.Reset()
				multiLineAttrQuote = 0

				// Continue processing the rest of this line for more attributes
				line = line[closingIdx+1:]
			} else {
				// No closing quote on this line, add entire line
				multiLineAttrContent.WriteString(line)
				multiLineAttrContent.WriteString("\n")
				continue
			}
		}

		// Process single-line attributes
		// First extract all single-line attributes
		for _, match := range attrPatternDQ.FindAllStringSubmatch(line, -1) {
			if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
				code := decodeHTMLEntities(match[1])
				sources = append(sources, HyperscriptSource{
					File:       filename,
					Line:       lineNum,
					SourceType: "attribute",
					Code:       code,
				})
			}
		}

		for _, match := range attrPatternSQ.FindAllStringSubmatch(line, -1) {
			if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
				code := decodeHTMLEntities(match[1])
				sources = append(sources, HyperscriptSource{
					File:       filename,
					Line:       lineNum,
					SourceType: "attribute",
					Code:       code,
				})
			}
		}

		// Remove single-line attributes to check for multi-line start
		lineWithoutSingleLine := attrPatternDQ.ReplaceAllString(line, "")
		lineWithoutSingleLine = attrPatternSQ.ReplaceAllString(lineWithoutSingleLine, "")

		// Check for start of multi-line attribute in remaining line
		dqIdx := strings.Index(lineWithoutSingleLine, `_="`)
		sqIdx := strings.Index(lineWithoutSingleLine, `_='`)

		if dqIdx >= 0 && (sqIdx < 0 || dqIdx < sqIdx) {
			// Double-quoted multi-line attribute starts
			startIdx := dqIdx + 3
			remaining := lineWithoutSingleLine[startIdx:]

			// Multi-line attribute
			inMultiLineAttr = true
			multiLineAttrStart = lineNum
			multiLineAttrQuote = '"'
			multiLineAttrContent.WriteString(remaining)
			multiLineAttrContent.WriteString("\n")

		} else if sqIdx >= 0 {
			// Single-quoted multi-line attribute starts
			startIdx := sqIdx + 3
			remaining := lineWithoutSingleLine[startIdx:]

			// Multi-line attribute
			inMultiLineAttr = true
			multiLineAttrStart = lineNum
			multiLineAttrQuote = '\''
			multiLineAttrContent.WriteString(remaining)
			multiLineAttrContent.WriteString("\n")
		}
	}

	// Handle unclosed multi-line attribute at EOF
	if inMultiLineAttr {
		finalContent := strings.TrimSpace(multiLineAttrContent.String())
		if finalContent != "" {
			code := decodeHTMLEntities(finalContent)
			sources = append(sources, HyperscriptSource{
				File:       filename,
				Line:       multiLineAttrStart,
				SourceType: "attribute",
				Code:       code,
			})
		}
	}

	return sources, scanner.Err()
}

// findClosingQuote finds the index of the closing quote, handling escaped quotes
func findClosingQuote(s string, quote byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == quote {
			// Check if it's escaped
			backslashCount := 0
			for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
				backslashCount++
			}
			// If odd number of backslashes, quote is escaped
			if backslashCount%2 == 0 {
				return i
			}
		}
	}
	return -1
}

// decodeHTMLEntities converts common HTML entities back to their characters.
func decodeHTMLEntities(s string) string {
	replacements := map[string]string{
		"&quot;": `"`,
		"&#34;":  `"`,
		"&apos;": `'`,
		"&#39;":  `'`,
		"&lt;":   `<`,
		"&#60;":  `<`,
		"&gt;":   `>`,
		"&#62;":  `>`,
		"&amp;":  `&`,
		"&#38;":  `&`,
	}

	result := s
	for entity, char := range replacements {
		result = strings.ReplaceAll(result, entity, char)
	}
	return result
}

// validateHyperscript runs the hyperscript parser on extracted code.
func validateHyperscript(vm *goja.Runtime, src HyperscriptSource) ValidationResult {
	result := ValidationResult{
		File:       src.File,
		Line:       src.Line,
		SourceType: src.SourceType,
		Code:       src.Code,
		Valid:      false,
	}

	validateFn, ok := goja.AssertFunction(vm.Get("__validateHyperscript"))
	if !ok {
		result.Error = "validator function not found"
		return result
	}

	res, err := validateFn(goja.Undefined(), vm.ToValue(src.Code))
	if err != nil {
		result.Error = fmt.Sprintf("validation call failed: %v", err)
		return result
	}

	obj := res.ToObject(vm)
	if valid := obj.Get("valid"); valid != nil && valid.ToBoolean() {
		result.Valid = true
	} else if errMsg := obj.Get("error"); errMsg != nil {
		result.Error = errMsg.String()
	}

	return result
}

// HyperscriptFeature represents a parsed feature from a hyperscript snippet.
type HyperscriptFeature struct {
	Keyword string `json:"keyword"` // "behavior" or "install"
	Name    string `json:"name"`
}

// FeatureRef associates a feature with its source location.
type FeatureRef struct {
	Kind string // "behavior" or "install"
	Name string
	File string
	Line int
}

// ForwardRefIssue represents a forward-reference problem.
type ForwardRefIssue struct {
	Type   string `json:"type"` // "error" or "warning"
	File   string `json:"file"`
	Line   int    `json:"line"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// extractFeatures calls the JS __extractHyperscriptFeatures on a snippet and
// returns the list of features found.
func extractFeatures(vm *goja.Runtime, src HyperscriptSource) ([]HyperscriptFeature, error) {
	extractFn, ok := goja.AssertFunction(vm.Get("__extractHyperscriptFeatures"))
	if !ok {
		return nil, fmt.Errorf("__extractHyperscriptFeatures not found")
	}

	res, err := extractFn(goja.Undefined(), vm.ToValue(src.Code))
	if err != nil {
		return nil, fmt.Errorf("extract call failed: %w", err)
	}

	var features []HyperscriptFeature
	if err := json.Unmarshal([]byte(res.String()), &features); err != nil {
		return nil, fmt.Errorf("unmarshal features: %w", err)
	}

	return features, nil
}

// checkGoTemplateEscaping checks for hyperscript code in <script type="text/hyperscript">
// blocks that contains `<` characters Go's html/template will escape to `&amp;lt;` at
// render time. Go only treats `<` as a tag start when followed by valid tag-name
// characters (letters, /, !, ?). When followed by other chars (like `.` in
// `<.class/>` selectors), Go escapes it, breaking hyperscript at runtime.
func checkGoTemplateEscaping(snippets []HyperscriptSource) []ForwardRefIssue {
	var issues []ForwardRefIssue
	for _, s := range snippets {
		if s.SourceType != "script_block" {
			continue
		}
		// Check for `<` followed by a character that doesn't start a valid HTML tag.
		// Valid starts: letters, /, !, ?
		for i := 0; i < len(s.Code); i++ {
			if s.Code[i] != '<' {
				continue
			}
			if i+1 >= len(s.Code) {
				continue
			}
			next := s.Code[i+1]
			isTagStart := (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') ||
				next == '/' || next == '!' || next == '?'
			if !isTagStart {
				// Get a snippet of context around the problematic `<`
				ctxStart := i - 10
				if ctxStart < 0 {
					ctxStart = 0
				}
				ctxEnd := i + 20
				if ctxEnd > len(s.Code) {
					ctxEnd = len(s.Code)
				}
				context := strings.TrimSpace(s.Code[ctxStart:ctxEnd])
				issues = append(issues, ForwardRefIssue{
					Type: "warning",
					File: s.File,
					Line: s.Line,
					Name: "go-escape",
					Detail: fmt.Sprintf(
						"Go html/template may escape `<` to `&amp;lt;` in script block: %s",
						context),
				})
				break // one warning per snippet is enough
			}
		}
	}
	return issues
}

// checkFileForwardReferences extracts features from all snippets in a file and
// checks for forward references (install before behavior). Returns issues.
// Install references whose behavior is not found in the same file are reported
// as WARNING (the behavior may be defined in another file like layout.html.tmpl).
func checkFileForwardReferences(vm *goja.Runtime, snippets []HyperscriptSource) []ForwardRefIssue {
	var issues []ForwardRefIssue
	var refs []FeatureRef

	for _, s := range snippets {
		features, err := extractFeatures(vm, s)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: feature extraction failed for %s:%d: %v\n", s.File, s.Line, err)
			continue
		}
		for _, f := range features {
			refs = append(refs, FeatureRef{
				Kind: f.Keyword,
				Name: f.Name,
				File: s.File,
				Line: s.Line,
			})
		}
	}

	if len(refs) == 0 {
		return nil
	}

	// Sort by line number. Stable sort preserves order within same line.
	sort.SliceStable(refs, func(i, j int) bool {
		return refs[i].Line < refs[j].Line
	})

	defined := make(map[string]bool) // behavior name -> defined

	for _, r := range refs {
		switch r.Kind {
		case "behavior":
			defined[r.Name] = true
		case "install":
			if !defined[r.Name] {
				// Behavior not found in this file — emit WARNING (might be in layout)
				issues = append(issues, ForwardRefIssue{
					Type:   "warning",
					File:   r.File,
					Line:   r.Line,
					Name:   r.Name,
					Detail: fmt.Sprintf("forward reference: install %q at line %d, behavior not found in same file", r.Name, r.Line),
				})
			}
		}
	}

	return issues
}

// printHumanReadable outputs the report in a format readable by both humans and AI.
func printHumanReadable(report ValidationReport, quiet bool) {
	fmt.Println("=" + strings.Repeat("=", 79))
	fmt.Println("HYPERSCRIPT VALIDATION REPORT")
	fmt.Println("=" + strings.Repeat("=", 79))
	fmt.Printf("Files scanned: %d | Scripts found: %d | Valid: %d | Invalid: %d | Warnings: %d\n",
		report.TotalFiles, report.TotalScripts, report.ValidCount, report.InvalidCount, report.FwdRefCount)
	fmt.Println(strings.Repeat("-", 80))

	for _, r := range report.Results {
		if quiet && r.Valid {
			continue
		}

		status := "✓ VALID"
		if !r.Valid {
			status = "✗ INVALID"
		}

		fmt.Printf("\n[%s] %s:%d (%s)\n", status, r.File, r.Line, r.SourceType)

		code := r.Code
		if len(code) > 100 {
			code = code[:97] + "..."
		}
		code = strings.ReplaceAll(code, "\n", "\\n")
		fmt.Printf("  Code: %s\n", code)

		if !r.Valid {
			fmt.Printf("  Error: %s\n", r.Error)
		}
	}

	// Print forward reference issues
	if len(report.FwdRefIssues) > 0 {
		fmt.Println()
		fmt.Println(strings.Repeat("-", 80))
		fmt.Println("FORWARD REFERENCE ISSUES")
		fmt.Println(strings.Repeat("-", 80))
		for _, issue := range report.FwdRefIssues {
			tag := "⚠ WARNING"
			if issue.Type == "error" {
				tag = "✗ ERROR"
			}
			fmt.Printf("\n[%s] %s:%d\n", tag, issue.File, issue.Line)
			fmt.Printf("  %s\n", issue.Detail)
		}
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 80))
	switch {
	case report.InvalidCount > 0:
		fmt.Printf("RESULT: FAILED (%d errors)\n", report.InvalidCount)
	case report.TotalScripts == 0:
		fmt.Println("RESULT: NO HYPERSCRIPT FOUND")
	default:
		fmt.Println("RESULT: PASSED")
	}
	if report.FwdRefCount > 0 {
		fmt.Printf("NOTE: %d forward reference warnings (behaviors may be defined in another file)\n", report.FwdRefCount)
	}
	fmt.Println(strings.Repeat("=", 80))
}
