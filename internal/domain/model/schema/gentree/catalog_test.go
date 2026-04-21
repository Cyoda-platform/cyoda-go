package gentree

import "testing"

func TestCatalogHasAtLeast40Fixtures(t *testing.T) {
	if n := len(Catalog); n < 40 {
		t.Fatalf("Catalog has %d fixtures, want >=40", n)
	}
}

func TestCatalogEntriesHaveRequiredFields(t *testing.T) {
	seen := make(map[string]struct{})
	for i, f := range Catalog {
		if f.Name == "" {
			t.Errorf("entry %d: empty Name", i)
		}
		if _, dup := seen[f.Name]; dup {
			t.Errorf("entry %d: duplicate Name %q", i, f.Name)
		}
		seen[f.Name] = struct{}{}
		if f.Old == nil && f.Incoming == nil {
			t.Errorf("%s: both Old and Incoming nil", f.Name)
		}
	}
}
