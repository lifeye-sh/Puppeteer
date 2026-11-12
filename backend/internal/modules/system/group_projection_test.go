package system

import (
	"next-terminal/backend/internal/modules/resource"
	"testing"
)

func TestGroupProjectionRejectsBrokenTopology(t *testing.T) {
	for _, groups := range [][]resource.Group{
		{{ID: "a", ParentID: "b"}},
		{{ID: "a", ParentID: "a"}},
		{{ID: "a", ParentID: "b"}, {ID: "b", ParentID: "a"}},
		{{ID: "a"}, {ID: "a"}},
	} {
		if _, e := resourceGroupTree(groups); e == nil {
			t.Fatal("invalid topology accepted")
		}
	}
	if tree, e := resourceGroupTree(nil); e != nil || tree != "[]" {
		t.Fatal(tree, e)
	}
}
