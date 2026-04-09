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
