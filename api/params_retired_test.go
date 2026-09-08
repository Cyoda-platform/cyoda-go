package api

import (
	"reflect"
	"testing"
)

// The consistency-wait flag is retired: a successful write response already
// means the write is visible to every subsequent read, so there is nothing
// for a flag to select. This pins that no generated params struct declares it
// again.
func TestNoParamsStructDeclaresWaitForConsistencyAfter(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(CreateParams{}),
		reflect.TypeOf(CreateCollectionParams{}),
		reflect.TypeOf(UpdateCollectionParams{}),
		reflect.TypeOf(UpdateSingleParams{}),
		reflect.TypeOf(UpdateSingleWithLoopbackParams{}),
		reflect.TypeOf(PatchSingleParams{}),
		reflect.TypeOf(PatchSingleWithLoopbackParams{}),
	} {
		if _, has := typ.FieldByName("WaitForConsistencyAfter"); has {
			t.Errorf("%s still declares WaitForConsistencyAfter", typ.Name())
		}
	}
}
