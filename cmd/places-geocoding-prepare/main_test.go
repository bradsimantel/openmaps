package main

import "testing"

func TestRequireDiskHeadroom(t *testing.T) {
	const floor = int64(60 << 30)
	if err := requireDiskHeadroom("output", 500<<30, 400<<30, floor); err != nil {
		t.Fatal(err)
	}
	if err := requireDiskHeadroom("output", 450<<30, 400<<30, floor); err == nil {
		t.Fatal("accepted build that would cross reserve")
	}
	if err := requireDiskHeadroom("output", 100<<30, 400<<30, floor); err == nil {
		t.Fatal("accepted estimate larger than available space")
	}
}
