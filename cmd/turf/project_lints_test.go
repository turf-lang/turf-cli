package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/turf-lang/turf-cli/internal/driver"
	"github.com/turf-lang/turf-cli/internal/version"
)

// The manifest's lint flags go after the fixed arguments and before the
// user's, so a level typed on the command line replaces the table's (the
// compiler keeps the last level it reads for a code).
func TestWithLintsOrder(t *testing.T) {
	p := &projectBuild{lints: []string{"--lint-set", "2.1", "--deny", "E163"}}
	got := p.withLints([]string{"--package-graph", "g", "src/main.tr"}, []string{"--allow", "E163", "-O2"})
	want := []string{"--package-graph", "g", "src/main.tr", "--lint-set", "2.1", "--deny", "E163", "--allow", "E163", "-O2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withLints = %v, want %v", got, want)
	}

	none := &projectBuild{}
	if got := none.withLints([]string{"a"}, []string{"b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("a project with no [lints] must pass the flags through unchanged, got %v", got)
	}
}

func TestLintInterfaceError(t *testing.T) {
	need := version.RequiredForLints
	for _, tc := range []struct {
		got version.Interface
		ok  bool
	}{
		{version.Interface{Major: 1, Minor: 7}, false},
		{need, true},
		{version.Interface{Major: need.Major, Minor: need.Minor + 1}, true},
		{version.Interface{Major: need.Major + 1, Minor: 0}, false},
	} {
		err := lintInterfaceError(driver.CompilerInfo{Release: "2.0.0", Interface: tc.got}, "/x/turfc")
		if (err == nil) != tc.ok {
			t.Errorf("interface %+v: ok=%v, err=%v", tc.got, tc.ok, err)
		}
		if err != nil && !strings.Contains(err.Error(), "[lints]") {
			t.Errorf("error %q does not name the table that needs the interface", err)
		}
	}
}
