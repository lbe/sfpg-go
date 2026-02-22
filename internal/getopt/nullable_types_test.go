package getopt

import "testing"

// TestOptInt_ZeroValue tests that OptInt zero value is correct
func TestOptInt_ZeroValue(t *testing.T) {
	var optInt OptInt
	if optInt.Int != 0 {
		t.Errorf("expected zero value Int=0, got %d", optInt.Int)
	}
	if optInt.IsSet != false {
		t.Errorf("expected zero value IsSet=false, got %v", optInt.IsSet)
	}
}

// TestOptBool_ZeroValue tests that OptBool zero value is correct
func TestOptBool_ZeroValue(t *testing.T) {
	var optBool OptBool
	if optBool.Bool != false {
		t.Errorf("expected zero value Bool=false, got %v", optBool.Bool)
	}
	if optBool.IsSet != false {
		t.Errorf("expected zero value IsSet=false, got %v", optBool.IsSet)
	}
}

// TestOptString_ZeroValue tests that OptString zero value is correct
func TestOptString_ZeroValue(t *testing.T) {
	var optString OptString
	if optString.String != "" {
		t.Errorf("expected zero value String=\"\", got %q", optString.String)
	}
	if optString.IsSet != false {
		t.Errorf("expected zero value IsSet=false, got %v", optString.IsSet)
	}
}
