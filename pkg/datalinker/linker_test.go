package datalinker

import (
	"reflect"
	"testing"
)

func TestMergeOverlappingIdentifierGroupsBuildsConnectedComponents(t *testing.T) {
	groups := [][]string{
		{"a", "b"},
		{"d", "e"},
		{"b", "c"},
		{"x"},
		{"c", "d"},
	}

	merged := mergeOverlappingIdentifierGroups(groups)
	expected := [][]string{
		{"a", "b", "c", "d", "e"},
		{"x"},
	}
	if !reflect.DeepEqual(merged, expected) {
		t.Fatalf("expected %v, got %v", expected, merged)
	}
}

func TestMergeOverlappingIdentifierGroupsRemovesDuplicates(t *testing.T) {
	merged := mergeOverlappingIdentifierGroups([][]string{{"a", "a", "b"}, {"b", "c"}})
	expected := [][]string{{"a", "b", "c"}}
	if !reflect.DeepEqual(merged, expected) {
		t.Fatalf("expected %v, got %v", expected, merged)
	}
}
