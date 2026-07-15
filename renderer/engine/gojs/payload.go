package gojs

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"

	"github.com/dop251/goja"
)

const maxNativePayloadDepth = 100

type payloadVisit struct {
	typ reflect.Type
	ptr uintptr
}

// nativePayloadValue 把常见 JSON 值复制成 goja 原生对象，避免 JS 反向修改 Go map/slice。
// 遇到需要 json tag、MarshalJSON 等语义的类型时返回 native=false，由调用方回退 JSON.parse。
func nativePayloadValue(rt *goja.Runtime, value any) (result goja.Value, native bool, err error) {
	return nativePayloadValueAtDepth(rt, value, make(map[payloadVisit]struct{}), 0)
}

func nativePayloadValueAtDepth(rt *goja.Runtime, value any, seen map[payloadVisit]struct{}, depth int) (goja.Value, bool, error) {
	if depth > maxNativePayloadDepth {
		return nil, false, fmt.Errorf("SSR payload exceeds maximum nesting depth %d", maxNativePayloadDepth)
	}

	switch typed := value.(type) {
	case nil:
		return goja.Null(), true, nil
	case bool, string:
		return rt.ToValue(typed), true, nil
	case int:
		return rt.ToValue(float64(typed)), true, nil
	case int8:
		return rt.ToValue(float64(typed)), true, nil
	case int16:
		return rt.ToValue(float64(typed)), true, nil
	case int32:
		return rt.ToValue(float64(typed)), true, nil
	case int64:
		return rt.ToValue(float64(typed)), true, nil
	case uint:
		return rt.ToValue(float64(typed)), true, nil
	case uint8:
		return rt.ToValue(float64(typed)), true, nil
	case uint16:
		return rt.ToValue(float64(typed)), true, nil
	case uint32:
		return rt.ToValue(float64(typed)), true, nil
	case uint64:
		return rt.ToValue(float64(typed)), true, nil
	case float32:
		value := float64(typed)
		if math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, false, fmt.Errorf("unsupported float value %v", value)
		}
		return rt.ToValue(value), true, nil
	case float64:
		if math.IsInf(typed, 0) || math.IsNaN(typed) {
			return nil, false, fmt.Errorf("unsupported float value %v", typed)
		}
		return rt.ToValue(typed), true, nil
	case json.Number:
		value, err := strconv.ParseFloat(string(typed), 64)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, false, fmt.Errorf("invalid JSON number %q", typed)
		}
		return rt.ToValue(value), true, nil
	case map[string]any:
		if typed == nil {
			return goja.Null(), true, nil
		}
		visit := payloadVisit{typ: reflect.TypeOf(typed), ptr: reflect.ValueOf(typed).Pointer()}
		if visit.ptr != 0 {
			if _, exists := seen[visit]; exists {
				return nil, false, fmt.Errorf("cyclic SSR payload")
			}
			seen[visit] = struct{}{}
			defer delete(seen, visit)
		}

		object := rt.NewObject()
		for key, item := range typed {
			itemValue, native, err := nativePayloadValueAtDepth(rt, item, seen, depth+1)
			if err != nil || !native {
				return nil, native, err
			}
			if err := object.DefineDataProperty(key, itemValue, goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE); err != nil {
				return nil, false, err
			}
		}
		return object, true, nil
	case []any:
		if typed == nil {
			return goja.Null(), true, nil
		}
		visit := payloadVisit{typ: reflect.TypeOf(typed), ptr: reflect.ValueOf(typed).Pointer()}
		if visit.ptr != 0 {
			if _, exists := seen[visit]; exists {
				return nil, false, fmt.Errorf("cyclic SSR payload")
			}
			seen[visit] = struct{}{}
			defer delete(seen, visit)
		}

		items := make([]any, len(typed))
		for index, item := range typed {
			itemValue, native, err := nativePayloadValueAtDepth(rt, item, seen, depth+1)
			if err != nil || !native {
				return nil, native, err
			}
			items[index] = itemValue
		}
		return rt.NewArray(items...), true, nil
	default:
		return nil, false, nil
	}
}
