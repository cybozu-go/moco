package mycnf

import (
	_ "embed"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestGenerator(t *testing.T) {
	t.Run("nil", testGeneratorNil)
	t.Run("normalize", testNormalize)
	t.Run("loose", testLoose)
}

//go:embed testdata/nil.cnf
var nilCnf string

func testGeneratorNil(t *testing.T) {
	actual := Generate(nil)
	if !cmp.Equal(nilCnf, actual) {
		t.Error("not matched", cmp.Diff(nilCnf, actual))
	}
}

//go:embed testdata/normalize.cnf
var normalizeCnf string

func testNormalize(t *testing.T) {
	actual := Generate(map[string]string{
		"thread-cache-size": "200",
		"foo":               "bar",
	})
	if !cmp.Equal(normalizeCnf, actual) {
		t.Error("not matched", cmp.Diff(normalizeCnf, actual))
	}
}

//go:embed testdata/loose.cnf
var looseCnf string

func testLoose(t *testing.T) {
	actual := Generate(map[string]string{
		"innodb_numa_interleave":                 "OFF",
		"loose_temptable_use_mmap":               "ON",
		"loose_innodb_validate_tablespace_paths": "ON",
	})
	if !cmp.Equal(looseCnf, actual) {
		t.Error("not matched", cmp.Diff(looseCnf, actual))
	}
}
