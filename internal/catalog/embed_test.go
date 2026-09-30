package catalog_test

import (
	"reflect"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// The embedded catalog must load identically to the on-disk one.
func TestLoadFSMatchesLoadDir(t *testing.T) {
	disk, err := catalog.Load("../../data")
	if err != nil {
		t.Fatal(err)
	}
	emb, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	if disk.Stats() != emb.Stats() {
		t.Fatalf("stats differ: disk %+v, embedded %+v", disk.Stats(), emb.Stats())
	}
	if !reflect.DeepEqual(disk.Macs, emb.Macs) || !reflect.DeepEqual(disk.Components, emb.Components) {
		t.Fatal("embedded catalog content differs from data/")
	}
}
