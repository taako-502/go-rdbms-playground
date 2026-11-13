package rdb

import (
	"cmp"
	"fmt"
	"slices"
)

// A minimum degree of 8 gives 7..15 keys per non-root node.
const treeDegree = 8

type key struct {
	Type Type   `json:"type"`
	Int  int64  `json:"int,omitempty"`
	Text string `json:"text,omitempty"`
	Bool bool   `json:"bool,omitempty"`
}

func makeKey(v any) key {
	switch x := v.(type) {
	case int64:
		return key{Type: Integer, Int: x}
	case string:
		return key{Type: String, Text: x}
	case bool:
		return key{Type: Boolean, Bool: x}
	default:
		panic("unvalidated index value")
	}
}
func (a key) compare(b key) int {
	if a.Type != b.Type {
		return cmp.Compare(a.Type, b.Type)
	}
	switch a.Type {
	case Integer:
		return cmp.Compare(a.Int, b.Int)
	case String:
		return cmp.Compare(a.Text, b.Text)
	case Boolean:
		if a.Bool == b.Bool {
			return 0
		}
		if !a.Bool {
			return -1
		}
		return 1
	}
	return 0
}
func (k key) valid(t Type) bool {
	return k.Type == t && ((t == Integer && k.Text == "" && !k.Bool) || (t == String && k.Int == 0 && !k.Bool) || (t == Boolean && k.Int == 0 && k.Text == ""))
}

type entry struct {
	Key     key     `json:"key"`
	Offsets []int64 `json:"offsets"`
}
type node struct {
	Entries  []entry `json:"entries"`
	Children []*node `json:"children,omitempty"`
}
type btree struct {
	Root *node `json:"root"`
}

func newTree() *btree { return &btree{Root: &node{}} }
func (n *node) position(k key) (int, bool) {
	return slices.BinarySearchFunc(n.Entries, k, func(e entry, k key) int { return e.Key.compare(k) })
}
func (t *btree) find(k key) *entry {
	for n := t.Root; n != nil; {
		i, ok := n.position(k)
		if ok {
			return &n.Entries[i]
		}
		if len(n.Children) == 0 {
			return nil
		}
		n = n.Children[i]
	}
	return nil
}
func (t *btree) search(k key) []int64 {
	if e := t.find(k); e != nil {
		return e.Offsets
	}
	return nil
}
func (t *btree) insert(k key, offset int64) {
	if e := t.find(k); e != nil {
		e.Offsets = append(e.Offsets, offset)
		return
	}
	if len(t.Root.Entries) == 2*treeDegree-1 {
		old := t.Root
		t.Root = &node{Children: []*node{old}}
		t.Root.split(0)
	}
	t.Root.insertNonFull(entry{k, []int64{offset}})
}
func (n *node) split(i int) {
	left := n.Children[i]
	mid := left.Entries[treeDegree-1]
	right := &node{Entries: append([]entry(nil), left.Entries[treeDegree:]...)}
	left.Entries = left.Entries[:treeDegree-1]
	if len(left.Children) > 0 {
		right.Children = append([]*node(nil), left.Children[treeDegree:]...)
		left.Children = left.Children[:treeDegree]
	}
	n.Entries = slices.Insert(n.Entries, i, mid)
	n.Children = slices.Insert(n.Children, i+1, right)
}
func (n *node) insertNonFull(e entry) {
	i, _ := n.position(e.Key)
	if len(n.Children) == 0 {
		n.Entries = slices.Insert(n.Entries, i, e)
		return
	}
	if len(n.Children[i].Entries) == 2*treeDegree-1 {
		n.split(i)
		if e.Key.compare(n.Entries[i].Key) > 0 {
			i++
		}
	}
	n.Children[i].insertNonFull(e)
}
func (t *btree) entries() []entry {
	out := make([]entry, 0)
	var walk func(*node)
	walk = func(n *node) {
		for i, e := range n.Entries {
			if len(n.Children) > 0 {
				walk(n.Children[i])
			}
			out = append(out, e)
		}
		if len(n.Children) > 0 {
			walk(n.Children[len(n.Entries)])
		}
	}
	walk(t.Root)
	return out
}
func (t *btree) validate(typ Type) error {
	leafDepth := -1
	var walk func(*node, bool, int, *key, *key) error
	walk = func(n *node, root bool, depth int, lower, upper *key) error {
		bad := func() error { return fmt.Errorf("%w: invalid B-tree", ErrCorrupt) }
		if n == nil || depth > 64 {
			return bad()
		}
		size := len(n.Entries)
		if size > 2*treeDegree-1 || (!root && size < treeDegree-1) || (root && size == 0 && len(n.Children) > 0) {
			return bad()
		}
		for i, e := range n.Entries {
			if !e.Key.valid(typ) || len(e.Offsets) == 0 {
				return bad()
			}
			if i > 0 && n.Entries[i-1].Key.compare(e.Key) >= 0 {
				return bad()
			}
			if lower != nil && e.Key.compare(*lower) <= 0 {
				return bad()
			}
			if upper != nil && e.Key.compare(*upper) >= 0 {
				return bad()
			}
			for j, off := range e.Offsets {
				if off < 0 || (j > 0 && off <= e.Offsets[j-1]) {
					return bad()
				}
			}
		}
		if len(n.Children) == 0 {
			if leafDepth < 0 {
				leafDepth = depth
			}
			if leafDepth != depth {
				return bad()
			}
			return nil
		}
		if len(n.Children) != size+1 {
			return bad()
		}
		for i, c := range n.Children {
			lo, hi := lower, upper
			if i > 0 {
				lo = &n.Entries[i-1].Key
			}
			if i < size {
				hi = &n.Entries[i].Key
			}
			if err := walk(c, false, depth+1, lo, hi); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(t.Root, true, 0, nil, nil)
}
