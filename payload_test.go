package gossr

import (
	"encoding/json"
	"math"
	"testing"
)

type countingPayloadMarshaler struct {
	calls *int
	value map[string]any
}

func (m countingPayloadMarshaler) MarshalJSON() ([]byte, error) {
	(*m.calls)++
	return json.Marshal(m.value)
}

func TestObjectPayloadMarshalsOnceAndDetachesSource(t *testing.T) {
	calls := 0
	sourceItems := []any{"first", map[string]any{"nested": true}}
	source := countingPayloadMarshaler{
		calls: &calls,
		value: map[string]any{
			"count": json.Number("9007199254740993"),
			"items": sourceItems,
		},
	}

	payload, err := ObjectPayload(source)
	if err != nil {
		t.Fatalf("ObjectPayload failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("MarshalJSON calls=%d, want 1", calls)
	}

	sourceItems[0] = "changed"
	object := payload.AsMap()
	if got := object["count"]; got != json.Number("9007199254740993") {
		t.Fatalf("number=%#v, want preserved json.Number", got)
	}
	items, ok := object["items"].([]any)
	if !ok || items[0] != "first" {
		t.Fatalf("payload was not detached from source: %#v", object)
	}

	// Consumers reuse the already-materialized graph and never invoke the
	// source marshaler again.
	_ = payload.AsMap()
	if calls != 1 {
		t.Fatalf("MarshalJSON calls after reuse=%d, want 1", calls)
	}
}

func TestObjectPayloadRejectsNonObjectAndInvalidJSONValues(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle

	tests := []struct {
		name  string
		value any
	}{
		{name: "nil", value: nil},
		{name: "null pointer", value: (*struct{})(nil)},
		{name: "array", value: []any{}},
		{name: "scalar", value: "value"},
		{name: "channel", value: map[string]any{"channel": make(chan int)}},
		{name: "cycle", value: cycle},
		{name: "nan", value: map[string]any{"number": math.NaN()}},
		{name: "positive infinity", value: map[string]any{"number": math.Inf(1)}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ObjectPayload(testCase.value); err == nil {
				t.Fatalf("ObjectPayload(%T) succeeded, want error", testCase.value)
			}
		})
	}
}
