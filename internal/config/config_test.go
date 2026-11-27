package config

import (
	"reflect"
	"testing"
)

func TestParseIDList(t *testing.T) {
	cases := map[string][]string{
		"":               nil,
		"  ":             nil,
		"123":            {"123"},
		" 123 , 456 ,, ": {"123", "456"},
	}
	for raw, want := range cases {
		if got := parseIDList(raw); !reflect.DeepEqual(got, want) {
			t.Errorf("parseIDList(%q) = %v, want %v", raw, got, want)
		}
	}
}
