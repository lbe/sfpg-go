package template

import "testing"

func TestAddAuthToData(t *testing.T) {
	tests := []struct {
		name            string
		data            map[string]any
		isAuthenticated bool
		wantNil         bool
		checkKey        string
		checkValue      any
	}{
		{
			name:            "nil data creates new map",
			data:            nil,
			isAuthenticated: false,
			wantNil:         false,
			checkKey:        "IsAuthenticated",
			checkValue:      false,
		},
		{
			name:            "authenticated user",
			data:            nil,
			isAuthenticated: true,
			wantNil:         false,
			checkKey:        "IsAuthenticated",
			checkValue:      true,
		},
		{
			name:            "preserves existing data",
			data:            map[string]any{"Key": "value"},
			isAuthenticated: true,
			wantNil:         false,
			checkKey:        "Key",
			checkValue:      "value",
		},
		{
			name:            "adds auth to existing data",
			data:            map[string]any{"Key": "value"},
			isAuthenticated: true,
			wantNil:         false,
			checkKey:        "IsAuthenticated",
			checkValue:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AddAuthToData(tt.data, tt.isAuthenticated)
			if (got == nil) != tt.wantNil {
				t.Errorf("AddAuthToData() nil = %v, want %v", got == nil, tt.wantNil)
				return
			}
			if got != nil && got[tt.checkKey] != tt.checkValue {
				t.Errorf("AddAuthToData()[%q] = %v, want %v", tt.checkKey, got[tt.checkKey], tt.checkValue)
			}
		})
	}
}

func TestAddCSRFToData(t *testing.T) {
	tests := []struct {
		name       string
		data       map[string]any
		csrfToken  string
		wantNil    bool
		checkKey   string
		checkValue any
	}{
		{
			name:       "nil data creates new map",
			data:       nil,
			csrfToken:  "test-token",
			wantNil:    false,
			checkKey:   "CSRFToken",
			checkValue: "test-token",
		},
		{
			name:       "preserves existing data",
			data:       map[string]any{"Key": "value"},
			csrfToken:  "test-token",
			wantNil:    false,
			checkKey:   "Key",
			checkValue: "value",
		},
		{
			name:       "adds CSRF to existing data",
			data:       map[string]any{"Key": "value"},
			csrfToken:  "test-token",
			wantNil:    false,
			checkKey:   "CSRFToken",
			checkValue: "test-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AddCSRFToData(tt.data, tt.csrfToken)
			if (got == nil) != tt.wantNil {
				t.Errorf("AddCSRFToData() nil = %v, want %v", got == nil, tt.wantNil)
				return
			}
			if got != nil && got[tt.checkKey] != tt.checkValue {
				t.Errorf("AddCSRFToData()[%q] = %v, want %v", tt.checkKey, got[tt.checkKey], tt.checkValue)
			}
		})
	}
}

func TestAddCommonData(t *testing.T) {
	tests := []struct {
		name            string
		data            map[string]any
		isAuthenticated bool
		csrfToken       string
		wantNil         bool
		wantAuth        bool
		wantCSRF        string
	}{
		{
			name:            "nil data creates new map",
			data:            nil,
			isAuthenticated: true,
			csrfToken:       "test-token",
			wantNil:         false,
			wantAuth:        true,
			wantCSRF:        "test-token",
		},
		{
			name:            "preserves existing data",
			data:            map[string]any{"Key": "value"},
			isAuthenticated: false,
			csrfToken:       "another-token",
			wantNil:         false,
			wantAuth:        false,
			wantCSRF:        "another-token",
		},
		{
			name:            "adds both to existing data",
			data:            map[string]any{"Key": "value"},
			isAuthenticated: true,
			csrfToken:       "test-token",
			wantNil:         false,
			wantAuth:        true,
			wantCSRF:        "test-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AddCommonData(tt.data, tt.isAuthenticated, tt.csrfToken)
			if (got == nil) != tt.wantNil {
				t.Errorf("AddCommonData() nil = %v, want %v", got == nil, tt.wantNil)
				return
			}
			if got == nil {
				return
			}
			if got["IsAuthenticated"] != tt.wantAuth {
				t.Errorf("AddCommonData()[IsAuthenticated] = %v, want %v", got["IsAuthenticated"], tt.wantAuth)
			}
			if got["CSRFToken"] != tt.wantCSRF {
				t.Errorf("AddCommonData()[CSRFToken] = %v, want %v", got["CSRFToken"], tt.wantCSRF)
			}
			if tt.data != nil && got["Key"] != "value" {
				t.Errorf("AddCommonData()[Key] = %v, want %v", got["Key"], "value")
			}
		})
	}
}
