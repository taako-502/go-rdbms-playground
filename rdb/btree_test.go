package rdb

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestBTreeSplits(t *testing.T) {
	for _, order := range []string{"ascending", "descending", "random"} {
		t.Run(order, func(t *testing.T) {
			n := 5000
			values := rand.New(rand.NewSource(42)).Perm(n)
			if order != "random" {
				for i := range values {
					values[i] = i
					if order == "descending" {
						values[i] = n - 1 - i
					}
				}
			}
			tree := newTree()
			expected := map[int64][]int64{}
			for off, v := range values {
				k := int64(v)
				tree.insert(makeKey(k), int64(off))
				expected[k] = []int64{int64(off)}
			}
			for v := range n {
				k := int64(v)
				tree.insert(makeKey(k), int64(n+v))
				expected[k] = append(expected[k], int64(n+v))
			}
			if err := tree.validate(Integer); err != nil {
				t.Fatal(err)
			}
			for k, want := range expected {
				if got := tree.search(makeKey(k)); !reflect.DeepEqual(got, want) {
					t.Fatalf("key %d: %v", k, got)
				}
			}
			if tree.search(makeKey(int64(-1))) != nil {
				t.Fatal("found absent key")
			}
			if len(tree.Root.Children) == 0 || len(tree.Root.Children[0].Children) == 0 {
				t.Fatal("expected multiple levels")
			}
		})
	}
}
func TestBTreeTypes(t *testing.T) {
	for _, vals := range [][]any{{"z", "a", "日本語"}, {false, true}} {
		tree := newTree()
		for i, v := range vals {
			tree.insert(makeKey(v), int64(i))
		}
		if err := tree.validate(makeKey(vals[0]).Type); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			if len(tree.search(makeKey(v))) != 1 {
				t.Fatal(v)
			}
		}
	}
}
func TestBTreeRejectMalformed(t *testing.T) {
	tree := newTree()
	tree.Root.Children = []*node{nil}
	if tree.validate(Integer) == nil {
		t.Fatal("accepted invalid tree")
	}
}
