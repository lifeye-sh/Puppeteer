package guac

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestBatchReaderOrderBoundsAndSync(t *testing.T) {
	input := Encode("name", "中文桌面") + strings.Repeat(Encode("rect", "0", "1", "2", "3", "4"), 300) + Encode("sync", "1") + Encode("name", strings.Repeat("a", 70000)) + Encode("sync", "2")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := NewBatchReader(ctx, NewReader(strings.NewReader(input)))
	var output strings.Builder
	batches, instructions := 0, 0
	for {
		batch, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		batches++
		size := 0
		if len(batch) > 128 {
			t.Fatal("unbounded batch")
		}
		for i, p := range batch {
			if p[0] == "sync" && i != len(batch)-1 {
				t.Fatal("crossed sync boundary")
			}
			encoded := Encode(p...)
			size += len(encoded)
			output.WriteString(encoded)
			instructions++
		}
		if size > 64*1024 && len(batch) != 1 {
			t.Fatal("byte budget exceeded")
		}
	}
	if output.String() != input {
		t.Fatal("instruction data or ordering changed")
	}
	if batches >= instructions {
		t.Fatal("no batching")
	}
	if _, err := reader.Read(); err != io.EOF {
		t.Fatal("EOF not stable", err)
	}
}

func TestBatchReaderFlushesWithoutSync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	reader := NewBatchReader(ctx, NewReader(r))
	go func() { io.WriteString(w, Encode("name", "idle")) }()
	result := make(chan [][]string, 1)
	go func() { batch, _ := reader.Read(); result <- batch }()
	select {
	case b := <-result:
		if len(b) != 1 || b[0][1] != "idle" {
			t.Fatal(b)
		}
	case <-time.After(time.Second):
		t.Fatal("missing bounded timer flush")
	}
}
