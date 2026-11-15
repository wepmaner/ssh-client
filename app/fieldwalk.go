package app

import (
	"errors"
	"fmt"
	"reflect"
)

// fieldStep is one field on a path through unexported struct fields: its name and the type string
// of its value (the dynamic type for an interface field).
type fieldStep struct{ name, typ string }

// errFieldNil is a path that runs through a nil pointer or interface before its end.
var errFieldNil = errors.New("nil on the field path")

// walkFields follows path from v, through pointers and interfaces, and returns the last field. It
// only reads: the value it returns is read-only for reflect. A field that is missing or of another
// type is an error naming it, so a changed dependency fails loudly instead of being misread.
func walkFields(v reflect.Value, path []fieldStep) (reflect.Value, error) {
	cur := v
	for i, s := range path {
		for cur.Kind() == reflect.Interface || cur.Kind() == reflect.Pointer {
			if cur.IsNil() {
				return reflect.Value{}, fmt.Errorf("%w: before %s", errFieldNil, s.name)
			}
			cur = cur.Elem()
		}
		if cur.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("field path: %s is a %s, not a struct", stepName(path, i), cur.Type())
		}
		f := cur.FieldByName(s.name)
		if !f.IsValid() {
			return reflect.Value{}, fmt.Errorf("field path: %s has no field %s", cur.Type(), s.name)
		}
		t := f.Type()
		if f.Kind() == reflect.Interface {
			if f.IsNil() {
				return reflect.Value{}, fmt.Errorf("%w: %s", errFieldNil, s.name)
			}
			t = f.Elem().Type()
		}
		if t.String() != s.typ {
			return reflect.Value{}, fmt.Errorf("field path: %s.%s is %s, want %s", cur.Type(), s.name, t, s.typ)
		}
		cur = f
	}
	return cur, nil
}

func stepName(path []fieldStep, i int) string {
	if i == 0 {
		return "the start"
	}
	return path[i-1].name
}
