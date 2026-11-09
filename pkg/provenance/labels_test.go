package provenance

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestLegacyToolLabelsReadAsContentLabels(t *testing.T) {
	var got Labels
	if err := json.Unmarshal([]byte(`["reads-untrusted","reads-private","credential-bearing","exec"]`), &got); err != nil {
		t.Fatal(err)
	}
	want := Labels{LabelUntrusted, LabelConfidential, LabelSecret, CapExec}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := NewLabels("reads-private"); !got.Has(LabelConfidential) || got.Has("reads-private") {
		t.Fatalf("legacy label should be mapped on Add, got %v", got)
	}
}
