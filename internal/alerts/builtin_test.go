package alerts

import "testing"

func TestBuiltinsParse(t *testing.T) {
	rules, err := LoadRules("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != len(allBuiltins) {
		t.Fatalf("loaded %d of %d built-ins", len(rules), len(allBuiltins))
	}
}
