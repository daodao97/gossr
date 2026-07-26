package gossr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// SSRPayload 表示可注入到 SSR 渲染/客户端的序列化数据。
type SSRPayload interface {
	AsMap() map[string]any
}

type objectPayload map[string]any

func (p objectPayload) AsMap() map[string]any {
	return p
}

// maxJSSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER. Integers beyond it
// silently lose precision when they cross into the JS renderer or the browser.
const maxJSSafeInteger = int64(1<<53 - 1)

// ObjectPayload converts a typed value into an immutable-by-convention JSON
// object payload. The source value is marshaled exactly once; all SSR
// consumers reuse the detached map/slice graph returned here.
//
// Payload numbers must survive JavaScript: every number is validated to be
// finite and, when integral, within ±2^53-1. Hosts with larger identifiers
// must encode them as strings.
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

	object, ok := decoded.(map[string]any)
	if !ok || object == nil {
		return nil, errors.New("object payload must encode a non-null JSON object")
	}
	if err := validateJSSafeNumbers(decoded, "$"); err != nil {
		return nil, err
	}
	return objectPayload(object), nil
}

func validateJSSafeNumbers(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if err := validateJSSafeNumbers(child, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := validateJSSafeNumbers(child, path+"["+strconv.Itoa(index)+"]"); err != nil {
				return err
			}
		}
	case json.Number:
		number, err := strconv.ParseFloat(typed.String(), 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return fmt.Errorf("invalid payload number at %s", path)
		}
		if math.Trunc(number) == number &&
			(number < -float64(maxJSSafeInteger) || number > float64(maxJSSafeInteger)) {
			return fmt.Errorf("payload integer at %s exceeds JavaScript safe precision", path)
		}
	}
	return nil
}
