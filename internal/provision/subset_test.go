package provision

import (
	"reflect"
	"testing"
)

// Every resource map on Stack has a kind, or rollback cannot see it.
func TestEveryStackFieldHasAKind(t *testing.T) {
	typ := reflect.TypeOf(Stack{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.Map {
			continue
		}
		if _, ok := kinds[f.Name]; !ok {
			t.Errorf("Stack.%s is a resource map with no entry in kinds: a rollback would never delete one", f.Name)
		}
	}
	for name := range kinds {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("kinds names Stack.%s, which does not exist", name)
		}
	}
}

func TestSubsetPicksAndLeavesTheOriginalAlone(t *testing.T) {
	s := &Stack{
		Queues: map[string]Queue{"a": {}, "b": {}},
		Topics: map[string]Topic{"t": {}},
	}
	got := Subset(s, func(kind, name string) bool { return kind == "queue" && name == "b" })
	if len(got.Queues) != 1 || len(got.Topics) != 0 {
		t.Fatalf("subset = %d queues, %d topics; want the one queue", len(got.Queues), len(got.Topics))
	}
	if _, ok := got.Queues["b"]; !ok {
		t.Fatal("the picked queue is not in the subset")
	}
	if len(s.Queues) != 2 || len(s.Topics) != 1 {
		t.Fatal("Subset changed the stack it was given")
	}
	names := Names(s)
	for _, want := range []string{"queue/a", "queue/b", "topic/t"} {
		if !names[want] {
			t.Errorf("Names lacks %s: %v", want, names)
		}
	}
	if len(names) != 3 {
		t.Errorf("Names = %v", names)
	}
}
