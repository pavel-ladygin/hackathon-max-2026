package sourceconfig

import "testing"

func TestCityBindings(t *testing.T) {
	for _, bad := range []string{
		`null`, `[]`, `{"generic:x":"bad"}`, `{"kudago":"3d3c83de-691b-510e-85e5-79685c4012ee"}`,
		`{"generic:x":"00000000-0000-0000-0000-000000000000"}`,
		`{"generic:x":"3d3c83de-691b-510e-85e5-79685c4012ee","generic:x":"3d3c83de-691b-510e-85e5-79685c4012ee"}`,
		`{} {}`, `{} garbage`,
	} {
		if _, err := ParseCityBindings(bad); err == nil {
			t.Fatalf("invalid binding accepted: %s", bad)
		}
	}
	for _, good := range []string{"", `{}`, `{"generic:kazan":"3d3c83de-691b-510e-85e5-79685c4012ee"}`} {
		if _, err := ParseCityBindings(good); err != nil {
			t.Fatal(err)
		}
	}
}
