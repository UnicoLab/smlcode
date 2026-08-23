package workspace

import (
	"encoding/json"
	"testing"
)

func TestIntArgCoercion(t *testing.T) {
	cases := []struct {
		name string
		val  interface{}
		def  int
		want int
	}{
		{"missing", nil, 7, 7},
		{"int", 12, 0, 12},
		{"int64", int64(12), 0, 12},
		{"float64", float64(200), 0, 200},
		{"float64 fractional", 200.9, 0, 200},
		{"string digits", "200", 0, 200},
		{"string padded", "  200 ", 0, 200},
		{"string float", "200.0", 0, 200},
		{"string garbage", "many", 5, 5},
		{"empty string", "", 5, 5},
		{"json.Number", json.Number("42"), 0, 42},
		{"bool true", true, 0, 1},
		{"map", map[string]interface{}{}, 3, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]interface{}{}
			if tc.val != nil {
				args["offset"] = tc.val
			}
			if got := intArg(args, "offset", tc.def); got != tc.want {
				t.Fatalf("intArg(%v)=%d want %d", tc.val, got, tc.want)
			}
		})
	}
}

func TestBoolArgCoercion(t *testing.T) {
	cases := []struct {
		name string
		val  interface{}
		def  bool
		want bool
	}{
		{"missing", nil, false, false},
		{"missing default true", nil, true, true},
		{"bool true", true, false, true},
		{"string true", "true", false, true},
		{"string True", "True", false, true},
		{"string TRUE", "TRUE", false, true},
		{"string 1", "1", false, true},
		{"string yes", "yes", false, true},
		{"string on", "on", false, true},
		{"string false", "false", true, false},
		{"string 0", "0", true, false},
		{"string no", "no", true, false},
		{"number 1", 1, false, true},
		{"number 0", 0, true, false},
		{"float 1", 1.0, false, true},
		{"garbage keeps default", "maybe", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]interface{}{}
			if tc.val != nil {
				args["replace_all"] = tc.val
			}
			if got := boolArg(args, "replace_all", tc.def); got != tc.want {
				t.Fatalf("boolArg(%v)=%v want %v", tc.val, got, tc.want)
			}
		})
	}
}

func TestStrArgCoercion(t *testing.T) {
	cases := []struct {
		name string
		val  interface{}
		want string
	}{
		{"missing", nil, ""},
		{"string", "pkg/a.go", "pkg/a.go"},
		{"int-valued float", float64(200), "200"},
		{"fractional float", 1.5, "1.5"},
		{"bool", true, "true"},
		{"json.Number", json.Number("9"), "9"},
		{"map", map[string]interface{}{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]interface{}{}
			if tc.val != nil {
				args["path"] = tc.val
			}
			if got := strArg(args, "path"); got != tc.want {
				t.Fatalf("strArg(%v)=%q want %q", tc.val, got, tc.want)
			}
		})
	}
}
