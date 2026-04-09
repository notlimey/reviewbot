package main

import (
	"encoding/json"
	"testing"
)

func TestRepairTruncatedJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "cut mid-string value",
			input: `{"issues": [{"category": "bug", "title": "something wro`,
		},
		{
			name:  "cut after comma",
			input: `{"issues": [{"category": "bug", "severity": "high", "title": "test"},`,
		},
		{
			name:  "cut mid-array",
			input: `{"issues": [{"category": "bug"}], "metadata": {"exports": ["foo", "bar"`,
		},
		{
			name:  "already valid",
			input: `{"issues": [], "metadata": {}}`,
		},
		{
			name:  "cut after key with no value",
			input: `{"issues": [], "metadata": {"exports":`,
		},
		{
			name:  "deeply nested truncation",
			input: `{"issues":[{"category":"bug","description":"Use fmt.Errorf with %w for wrapping errors instead of`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repaired := repairTruncatedJSON(tt.input)
			if !json.Valid([]byte(repaired)) {
				t.Errorf("repair did not produce valid JSON\n  input:    %s\n  repaired: %s", tt.input, repaired)
			}
		})
	}
}

func TestRepairDanglingKeyWithEscapedQuotes(t *testing.T) {
	// Bug 3: The old code used strings.LastIndex to find the opening quote of
	// a dangling key, which would match a quote inside a preceding string value.
	input := `{"issues":[],"metadata":{"summary":"a \"quoted\" thing","exports":`
	repaired := repairTruncatedJSON(input)
	if !json.Valid([]byte(repaired)) {
		t.Fatalf("repair did not produce valid JSON\n  input:    %s\n  repaired: %s", input, repaired)
	}

	// The repaired JSON should still contain the summary value.
	var m map[string]any
	if err := json.Unmarshal([]byte(repaired), &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	meta, ok := m["metadata"].(map[string]any)
	if !ok {
		t.Fatal("metadata missing from repaired JSON")
	}
	summary, ok := meta["summary"].(string)
	if !ok || summary != `a "quoted" thing` {
		t.Errorf("summary was lost or corrupted: got %q", summary)
	}
}

func TestFindStructuralEnd(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int // expected position, -1 for truncated
	}{
		{
			name:  "complete object",
			input: `{"a": 1}`,
			want:  7,
		},
		{
			name:  "complete with trailing text",
			input: `{"a": 1} here is more`,
			want:  7,
		},
		{
			name:  "truncated object",
			input: `{"a": 1, "b":`,
			want:  -1,
		},
		{
			name:  "brace inside string ignored",
			input: `{"a": "contains } brace"}`,
			want:  24,
		},
		{
			name:  "mismatched brackets not counted",
			input: `{"a": [1,2,3]}`,
			want:  13,
		},
		{
			name:  "nested arrays and objects",
			input: `{"a": [{"b": [1]}]}`,
			want:  18,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findStructuralEnd(tt.input)
			if got != tt.want {
				t.Errorf("findStructuralEnd(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestFindStructuralKey(t *testing.T) {
	tests := []struct {
		name  string
		input string
		key   string
		found bool
	}{
		{
			name:  "key at root",
			input: `{"issues": []}`,
			key:   "issues",
			found: true,
		},
		{
			name:  "key inside string value should not match",
			input: `{"description": "check the \"issues\" list", "issues": []}`,
			key:   "issues",
			found: true, // should find the real key, not the one in the string
		},
		{
			name:  "key only inside string value",
			input: `{"description": "issues are bad"}`,
			key:   "issues",
			found: false,
		},
		{
			name:  "key not present",
			input: `{"foo": "bar"}`,
			key:   "issues",
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := findStructuralKey(tt.input, tt.key)
			if tt.found && idx < 0 {
				t.Errorf("expected to find key %q but got -1", tt.key)
			}
			if !tt.found && idx >= 0 {
				t.Errorf("expected not to find key %q but got %d", tt.key, idx)
			}
		})
	}
}

func TestCleanJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already clean",
			input: `{"issues": []}`,
			want:  `{"issues": []}`,
		},
		{
			name:  "markdown fences",
			input: "```json\n{\"issues\": []}\n```",
			want:  `{"issues": []}`,
		},
		{
			name:  "leading text",
			input: `Here is my review: {"issues": []}`,
			want:  `{"issues": []}`,
		},
		{
			name:  "trailing text",
			input: `{"issues": []} I hope this helps!`,
			want:  `{"issues": []}`,
		},
		{
			name:  "truncated — does not trim",
			input: `{"issues": [{"title": "contains } brace"`,
			want:  `{"issues": [{"title": "contains } brace"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanJSON(tt.input)
			if got != tt.want {
				t.Errorf("cleanJSON(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFixNewlinesInStrings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "newline in string",
			input: "{\"a\": \"line1\nline2\"}",
			want:  `{"a": "line1\nline2"}`,
		},
		{
			name:  "crlf in string",
			input: "{\"a\": \"line1\r\nline2\"}",
			want:  `{"a": "line1\nline2"}`,
		},
		{
			name:  "standalone cr in string",
			input: "{\"a\": \"line1\rline2\"}",
			want:  `{"a": "line1\nline2"}`,
		},
		{
			name:  "tab in string",
			input: "{\"a\": \"col1\tcol2\"}",
			want:  `{"a": "col1\tcol2"}`,
		},
		{
			name:  "newline outside string unchanged",
			input: "{\n  \"a\": \"b\"\n}",
			want:  "{\n  \"a\": \"b\"\n}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fixNewlinesInStrings(tt.input)
			if got != tt.want {
				t.Errorf("fixNewlinesInStrings()\n  got:  %q\n  want: %q", got, tt.want)
			}
		})
	}
}

func TestParseScanResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "valid response",
			input: `{"issues":[],"metadata":{"exports":[],"imports":[],"interfaces":[],"patterns":[],"summary":"test"}}`,
		},
		{
			name:  "with markdown fences",
			input: "```json\n{\"issues\":[],\"metadata\":{\"exports\":[],\"imports\":[],\"interfaces\":[],\"patterns\":[],\"summary\":\"test\"}}\n```",
		},
		{
			name:  "with raw newlines in value",
			input: "{\"issues\":[],\"metadata\":{\"exports\":[],\"imports\":[],\"interfaces\":[],\"patterns\":[],\"summary\":\"line1\nline2\"}}",
		},
		{
			name:  "truncated but repairable",
			input: `{"issues":[{"category":"bug","severity":"high","confidence":0.9,"title":"test","description":"desc","line_start":1,"line_end":2,"suggestion":"fix"}],"metadata":{"exports":["Foo"],"imports":[{"from":"os","names":["ReadFile"]}],"inter`,
		},
		{
			name:    "total garbage",
			input:   `not json at all`,
			wantErr: true,
		},
		{
			name:  "truncated with raw newlines",
			input: "{\"issues\":[],\"metadata\":{\"exports\":[],\"imports\":[],\"interfaces\":[],\"patterns\":[],\"summary\":\"Handles file\ndiscovery and ha",
		},
		{
			name:  "numeric fields as strings",
			input: `{"issues":[{"category":"bug","severity":"high","confidence":"0.9","title":"test","description":"d","line_start":"10","line_end":"12","suggestion":"fix"}],"metadata":{"exports":[],"imports":[],"interfaces":[],"patterns":[],"summary":"x"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := parseScanResponse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if resp == nil {
				t.Error("got nil response")
			}
		})
	}
}

func TestScanIssueNumericStrings(t *testing.T) {
	input := `{"category":"bug","severity":"high","confidence":"0.85","title":"t","description":"d","line_start":"10","line_end":"12","suggestion":"s"}`
	var issue ScanIssue
	if err := json.Unmarshal([]byte(input), &issue); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if issue.Confidence != 0.85 {
		t.Errorf("confidence: got %f, want 0.85", issue.Confidence)
	}
	if issue.LineStart == nil || *issue.LineStart != 10 {
		t.Errorf("line_start: got %v, want 10", issue.LineStart)
	}
	if issue.LineEnd == nil || *issue.LineEnd != 12 {
		t.Errorf("line_end: got %v, want 12", issue.LineEnd)
	}
}

func TestExtractPartialScanResponse(t *testing.T) {
	// The key "issues" appears inside a string value — should not match it.
	input := `{"description":"fix issues here","issues":[{"category":"bug","severity":"high","confidence":0.9,"title":"test","description":"d","line_start":1,"line_end":1,"suggestion":"s"}],"metadata":{"truncated`
	repaired := repairTruncatedJSON(input)
	result := extractPartialScanResponse(repaired)
	if result == nil {
		t.Fatal("expected partial result, got nil")
	}
	if len(result.Issues) != 1 {
		t.Errorf("expected 1 issue, got %d", len(result.Issues))
	}
	if result.Issues[0].Category != "bug" {
		t.Errorf("expected category 'bug', got %q", result.Issues[0].Category)
	}
}
