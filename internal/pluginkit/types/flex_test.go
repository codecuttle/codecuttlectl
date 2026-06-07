package types

import (
	"encoding/json"
	"testing"
)

func TestFlexInt_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"integer", `42`, 42},
		{"zero", `0`, 0},
		{"negative", `-5`, -5},
		{"string integer", `"42"`, 42},
		{"string negative", `"-5"`, -5},
		{"string with spaces", `" 42 "`, 42},
		{"empty string", `""`, 0},
		{"non-numeric string", `"hello"`, 0},
		{"null", `null`, 0},
		{"float truncates via int unmarshal", `3.9`, 0}, // json.Unmarshal to int fails on float
		{"string float", `"3.9"`, 0},                    // strconv.Atoi fails on float strings
		{"large number", `999999`, 999999},
		{"string large number", `"999999"`, 999999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexInt
			if err := json.Unmarshal([]byte(tt.input), &f); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f.Int() != tt.want {
				t.Errorf("got %d, want %d", f.Int(), tt.want)
			}
		})
	}
}

func TestFlexInt_MarshalJSON(t *testing.T) {
	f := FlexInt(42)
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "42" {
		t.Errorf("got %s, want 42", string(data))
	}
}

func TestFlexInt_InStruct(t *testing.T) {
	type params struct {
		Timeout FlexInt `json:"timeout,omitempty"`
		Limit   FlexInt `json:"limit"`
	}

	tests := []struct {
		name    string
		input   string
		timeout int
		limit   int
	}{
		{"both integers", `{"timeout": 30, "limit": 100}`, 30, 100},
		{"both strings", `{"timeout": "30", "limit": "100"}`, 30, 100},
		{"mixed", `{"timeout": "30", "limit": 100}`, 30, 100},
		{"missing optional", `{"limit": 50}`, 0, 50},
		{"all missing", `{}`, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p params
			if err := json.Unmarshal([]byte(tt.input), &p); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Timeout.Int() != tt.timeout {
				t.Errorf("timeout: got %d, want %d", p.Timeout.Int(), tt.timeout)
			}
			if p.Limit.Int() != tt.limit {
				t.Errorf("limit: got %d, want %d", p.Limit.Int(), tt.limit)
			}
		})
	}
}

func TestFlexBool_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"true", `true`, true},
		{"false", `false`, false},
		{"string true", `"true"`, true},
		{"string false", `"false"`, false},
		{"string True", `"True"`, true},
		{"string FALSE", `"FALSE"`, false},
		{"string yes", `"yes"`, true},
		{"string no", `"no"`, false},
		{"string 1", `"1"`, true},
		{"string 0", `"0"`, false},
		{"integer 1", `1`, true},
		{"integer 0", `0`, false},
		{"integer nonzero", `42`, true},
		{"null", `null`, false},
		{"empty string", `""`, false},
		{"garbage string", `"maybe"`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexBool
			if err := json.Unmarshal([]byte(tt.input), &f); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f.Bool() != tt.want {
				t.Errorf("got %v, want %v", f.Bool(), tt.want)
			}
		})
	}
}

func TestFlexBool_MarshalJSON(t *testing.T) {
	tests := []struct {
		val  FlexBool
		want string
	}{
		{FlexBool(true), "true"},
		{FlexBool(false), "false"},
	}

	for _, tt := range tests {
		data, err := json.Marshal(tt.val)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(data) != tt.want {
			t.Errorf("got %s, want %s", string(data), tt.want)
		}
	}
}

func TestFlexBool_InStruct(t *testing.T) {
	type params struct {
		ReplaceAll FlexBool `json:"replace_all,omitempty"`
		Force      FlexBool `json:"force"`
	}

	tests := []struct {
		name       string
		input      string
		replaceAll bool
		force      bool
	}{
		{"booleans", `{"replace_all": true, "force": false}`, true, false},
		{"strings", `{"replace_all": "true", "force": "false"}`, true, false},
		{"integers", `{"replace_all": 1, "force": 0}`, true, false},
		{"missing", `{}`, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p params
			if err := json.Unmarshal([]byte(tt.input), &p); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.ReplaceAll.Bool() != tt.replaceAll {
				t.Errorf("replace_all: got %v, want %v", p.ReplaceAll.Bool(), tt.replaceAll)
			}
			if p.Force.Bool() != tt.force {
				t.Errorf("force: got %v, want %v", p.Force.Bool(), tt.force)
			}
		})
	}
}
