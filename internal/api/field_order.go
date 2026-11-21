package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// fieldOrderJSON 是各公开协议输出对象的官方字段顺序表
//
//go:embed field_order.json
var fieldOrderJSON []byte

// fieldOrder 是一个公开协议输出对象的官方字段顺序：Global 为全协议统一的先后，Kinds 按对象种类给出先后，TypeFirst 为 type 在前、其余按字母序
type fieldOrder struct {
	Global    []string            `json:"global"`
	Kinds     map[string][]string `json:"kinds"`
	TypeFirst bool                `json:"typeFirst"`
	Opaque    []string            `json:"opaque"`
	global    map[string]int
	kinds     map[string]map[string]int
}

// fieldOrders 是 gemini、chat、responses、anthropic 与 interactions 输出的字段顺序
var fieldOrders = loadFieldOrders()

// loadFieldOrders 解析内嵌的字段顺序表
func loadFieldOrders() map[string]*fieldOrder {
	var orders map[string]*fieldOrder
	if err := json.Unmarshal(fieldOrderJSON, &orders); err != nil {
		panic(err)
	}
	for _, order := range orders {
		order.global = fieldRanks(order.Global)
		order.kinds = make(map[string]map[string]int, len(order.Kinds))
		for kind, keys := range order.Kinds {
			order.kinds[kind] = fieldRanks(keys)
		}
	}
	return orders
}

// fieldRanks 返回字段名到先后位置的映射
func fieldRanks(keys []string) map[string]int {
	if len(keys) == 0 {
		return nil
	}
	ranks := make(map[string]int, len(keys))
	for index, key := range keys {
		ranks[key] = index
	}
	return ranks
}

// officialOrder 按官方字段顺序包装响应对象，供 JSON 编码使用
func officialOrder(protocol string, value any) any {
	return fieldOrders[protocol].value(value, "")
}

// orderedJSON 是按官方字段顺序编码的动态对象
type orderedJSON struct {
	fields map[string]any
	order  *fieldOrder
	parent string
}

// value 为动态字段准备嵌套对象，其余值交给 encoding/json
func (order *fieldOrder) value(value any, parent string) any {
	if slices.Contains(order.Opaque, parent) {
		return value
	}
	switch value := value.(type) {
	case nil:
		return json.RawMessage("null")
	case map[string]any:
		if value == nil {
			return value
		}
		fields := make(map[string]any, len(value))
		for key, item := range value {
			fields[key] = order.value(item, key)
		}
		return orderedJSON{fields: fields, order: order, parent: parent}
	case map[string]string:
		if value == nil {
			return value
		}
		fields := make(map[string]any, len(value))
		for key, item := range value {
			fields[key] = item
		}
		return orderedJSON{fields: fields, order: order, parent: parent}
	case map[string]json.RawMessage:
		if value == nil {
			return value
		}
		fields := make(map[string]any, len(value))
		for key, item := range value {
			fields[key] = item
		}
		return orderedJSON{fields: fields, order: order, parent: parent}
	case []map[string]any:
		if value == nil {
			return value
		}
		items := make([]any, len(value))
		for index, item := range value {
			items[index] = order.value(item, parent)
		}
		return items
	case []any:
		if value == nil {
			return value
		}
		items := make([]any, len(value))
		for index, item := range value {
			items[index] = order.value(item, parent)
		}
		return items
	}
	return value
}

// MarshalJSON 只排列当前动态对象的键，字段值由 encoding/json 编码
func (o orderedJSON) MarshalJSON() ([]byte, error) {
	keys := slices.Collect(maps.Keys(o.fields))
	ranks := o.order.ranksFor(o.fields, o.parent)
	slices.SortFunc(keys, func(a, b string) int { return o.order.compare(ranks, a, b) })
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		item, err := json.Marshal(o.fields[key])
		if err != nil {
			return nil, err
		}
		buffer.Write(name)
		buffer.WriteByte(':')
		buffer.Write(item)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

// ranksFor 返回对象所属种类的字段先后：Global 优先，否则按 type、object 或所在字段名查 Kinds
func (order *fieldOrder) ranksFor(object map[string]any, parent string) map[string]int {
	if order.global != nil {
		return order.global
	}
	if order.TypeFirst {
		return nil
	}
	for _, field := range []string{"type", "object"} {
		if name := mapString(object, field); name != "" {
			return order.kinds[field+":"+name]
		}
	}
	return order.kinds["key:"+parent]
}

// compare 比较同一对象中的两个字段：表中的字段按表的先后在前，其余按字母序
func (order *fieldOrder) compare(ranks map[string]int, a, b string) int {
	if order.TypeFirst && (a == "type") != (b == "type") {
		if a == "type" {
			return -1
		}
		return 1
	}
	left, leftKnown := ranks[a]
	right, rightKnown := ranks[b]
	switch {
	case leftKnown && rightKnown:
		return left - right
	case leftKnown:
		return -1
	case rightKnown:
		return 1
	}
	return strings.Compare(a, b)
}

// mapString 读取对象中字符串字段的值，原始 JSON 字段按 JSON 字符串解析
func mapString(object map[string]any, field string) string {
	switch value := object[field].(type) {
	case string:
		return value
	case json.RawMessage:
		var name string
		_ = json.Unmarshal(value, &name)
		return name
	}
	return ""
}
