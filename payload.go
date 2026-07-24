package gossr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// SSRPayload 表示可注入到 SSR 渲染/客户端的序列化数据。
type SSRPayload interface {
	AsMap() map[string]any
}

type objectPayload map[string]any

func (p objectPayload) AsMap() map[string]any {
	return p
}

// ObjectPayload converts a typed value into an immutable-by-convention JSON
// object payload. The source value is marshaled exactly once; all SSR
// consumers reuse the detached map/slice graph returned here.
func ObjectPayload(value any) (SSRPayload, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode object payload: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode object payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("decode object payload: multiple JSON values")
		}
		return nil, fmt.Errorf("decode object payload trailing data: %w", err)
	}

	object, ok := decoded.(map[string]any)
	if !ok || object == nil {
		return nil, errors.New("object payload must encode a non-null JSON object")
	}
	return objectPayload(object), nil
}
