package normalize

import (
	"encoding/json"
	"strconv"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// AttrMap flattens an OTLP key/value list into a typed lookup.
type AttrMap map[string]any

func attrMapFromKVList(kvs []*commonpb.KeyValue) AttrMap {
	m := make(AttrMap, len(kvs))
	for _, kv := range kvs {
		if kv == nil || kv.Value == nil {
			continue
		}
		m[kv.Key] = anyValue(kv.Value)
	}
	return m
}

func anyValue(v *commonpb.AnyValue) any {
	if v == nil {
		return nil
	}
	switch x := v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue
	case *commonpb.AnyValue_IntValue:
		return x.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue
	case *commonpb.AnyValue_BytesValue:
		return string(x.BytesValue)
	case *commonpb.AnyValue_ArrayValue:
		if x.ArrayValue == nil {
			return nil
		}
		out := make([]any, 0, len(x.ArrayValue.Values))
		for _, v := range x.ArrayValue.Values {
			out = append(out, anyValue(v))
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		if x.KvlistValue == nil {
			return nil
		}
		return attrMapFromKVList(x.KvlistValue.Values)
	}
	return nil
}

// String returns the attribute value rendered as a string. Missing keys yield "".
func (m AttrMap) String(key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// FirstString returns the first non-empty attribute among keys.
func (m AttrMap) FirstString(keys ...string) string {
	for _, k := range keys {
		if v := m.String(k); v != "" {
			return v
		}
	}
	return ""
}

func (m AttrMap) Int64(key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

func (m AttrMap) FirstInt64(keys ...string) int64 {
	for _, k := range keys {
		if v := m.Int64(k); v != 0 {
			return v
		}
	}
	return 0
}

func (m AttrMap) Float64(key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

// flatten copies non-empty primitive attributes into the run-level tag bag.
func (m AttrMap) flattenInto(dst map[string]string) {
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if x != "" {
				dst[k] = x
			}
		case bool:
			dst[k] = strconv.FormatBool(x)
		case int64:
			dst[k] = strconv.FormatInt(x, 10)
		case float64:
			dst[k] = strconv.FormatFloat(x, 'g', -1, 64)
		}
	}
}
