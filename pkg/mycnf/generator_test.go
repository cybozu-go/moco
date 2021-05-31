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
	t.Run("buffer-pool-size", testBufferPoolSize)
	t.Run("opaque", testOpaque)
}

//go:embed testdata/nil.cnf
var nilCnf string

func testGeneratorNil(t *testing.T) {
	actual := Generate(nil, 100<<20)
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
	}, 1000<<20)
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
	}, 1000<<20)
	if !cmp.Equal(looseCnf, actual) {
		t.Error("not matched", cmp.Diff(looseCnf, actual))
	}
}

//go:embed testdata/bufsize.cnf
var bufsizeCnf string

func testBufferPoolSize(t *testing.T) {
	actual := Generate(map[string]string{
		"innodb_buffer_pool_size": "268435456",
	}, 1000<<20)
	if !cmp.Equal(bufsizeCnf, actual) {
		t.Error("not matched", cmp.Diff(bufsizeCnf, actual))
	}
}

//go:embed testdata/opaque.cnf
var opaqueCnf string

func testOpaque(t *testing.T) {
	actual := Generate(map[string]string{
		"_include": `performance-schema-instrument='memory/%=ON'
performance-schema-instrument='wait/synch/%/innodb/%=ON'
performance-schema-instrument='wait/lock/table/sql/handler=OFF'
performance-schema-instrument='wait/lock/metadata/sql/mdl=OFF'
`}, 100<<20)
	if !cmp.Equal(opaqueCnf, actual) {
		t.Error("not matched", cmp.Diff(opaqueCnf, actual))
	}

}
