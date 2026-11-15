package app

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type walkLeaf struct{ n int }

type walkInner struct {
	leaf *walkLeaf
	on   bool
}

type walkImpl interface{ x() }

func (*walkInner) x() {}

type walkOuter struct{ impl walkImpl }

func TestWalkFields(t *testing.T) {
	leaf := &walkLeaf{n: 7}
	o := &walkOuter{impl: &walkInner{leaf: leaf, on: true}}
	path := []fieldStep{{"impl", "*app.walkInner"}, {"leaf", "*app.walkLeaf"}}
	v, err := walkFields(reflect.ValueOf(o), path)
	if err != nil {
		t.Fatal(err)
	}
	if v.UnsafePointer() != reflect.ValueOf(leaf).UnsafePointer() {
		t.Fatal("wrong pointer")
	}
	b, err := walkFields(reflect.ValueOf(o), []fieldStep{{"impl", "*app.walkInner"}, {"on", "bool"}})
	if err != nil || !b.Bool() {
		t.Fatalf("bool %v %v", b, err)
	}

	// A nil end is a value; a nil on the way is errFieldNil.
	o.impl.(*walkInner).leaf = nil
	if v, err := walkFields(reflect.ValueOf(o), path); err != nil || !v.IsNil() {
		t.Fatalf("nil end: %v", err)
	}
	if _, err := walkFields(reflect.ValueOf(&walkOuter{}), path); !errors.Is(err, errFieldNil) {
		t.Fatalf("nil interface: %v", err)
	}
	if _, err := walkFields(reflect.ValueOf((*walkOuter)(nil)), path); !errors.Is(err, errFieldNil) {
		t.Fatalf("nil start: %v", err)
	}

	// A missing field or a changed type fails with its name.
	for _, bad := range [][]fieldStep{
		{{"impl", "*app.walkInner"}, {"gone", "*app.walkLeaf"}},
		{{"impl", "*app.other"}, {"leaf", "*app.walkLeaf"}},
		{{"impl", "*app.walkInner"}, {"leaf", "*app.walkInner"}},
		{{"impl", "*app.walkInner"}, {"on", "bool"}, {"x", "int"}},
	} {
		_, err := walkFields(reflect.ValueOf(&walkOuter{impl: &walkInner{on: true}}), bad)
		if err == nil || errors.Is(err, errFieldNil) || !strings.Contains(err.Error(), "field path") {
			t.Fatalf("%v: %v", bad, err)
		}
	}
}
