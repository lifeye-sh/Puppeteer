package backup

import (
	"context"
	"math"
	"testing"
)

func TestDiskSpacePreflightFailsClosed(t *testing.T) {
	if e := RequireSpace(context.Background(), t.TempDir(), math.MaxUint64); e == nil {
		t.Fatal("insufficient free space accepted")
	}
	if e := RequireSpace(context.Background(), t.TempDir()+"/missing", 1); e == nil {
		t.Fatal("unmeasurable filesystem accepted")
	}
}
