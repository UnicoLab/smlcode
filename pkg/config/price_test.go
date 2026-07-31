package config

import "testing"

func TestPricePresetRates(t *testing.T) {
	cases := []struct {
		preset, provider string
		ok               bool
		free             bool
	}{
		{"", "omlx", false, false},
		{"off", "openai", false, false},
		{"local", "omlx", true, true},
		{"omlx", "", true, true},
		{"openai", "", true, false},
		{"anthropic", "", true, false},
		{"auto", "omlx", true, true},
		{"auto", "openai", true, false},
		{"mystery", "custom", false, false},
	}
	for _, tc := range cases {
		pin, cout, ok := PricePresetRates(tc.preset, tc.provider)
		if ok != tc.ok {
			t.Fatalf("%s/%s: ok=%v want %v", tc.preset, tc.provider, ok, tc.ok)
		}
		if tc.free && (pin != 0 || cout != 0) {
			t.Fatalf("%s: want $0 got %v/%v", tc.preset, pin, cout)
		}
		if tc.ok && !tc.free && pin <= 0 && cout <= 0 {
			t.Fatalf("%s: want positive ballpark", tc.preset)
		}
	}
}

func TestPriceRatesExplicitWins(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.PricePreset = "openai"
	cfg.PricePromptPerMTok = 1.25
	cfg.PriceCompletionPerMTok = 2.5
	pin, cout, ok := cfg.PriceRates()
	if !ok || pin != 1.25 || cout != 2.5 {
		t.Fatalf("got %v %v %v", pin, cout, ok)
	}
}
