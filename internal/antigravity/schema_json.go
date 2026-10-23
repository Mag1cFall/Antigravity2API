package antigravity

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
)

// schemaFieldOrder 为官方 language_server 内嵌 aiplatform master Schema 的字段声明顺序，protojson 按此顺序输出
var schemaFieldOrder = []string{
	"type", "format", "title", "description", "nullable", "default", "items", "prefixItems", "minItems", "maxItems",
	"enum", "properties", "propertyOrdering", "required", "minProperties", "maxProperties", "minimum", "maximum",
	"minLength", "maxLength", "pattern", "example", "oneOf", "anyOf", "allOf", "not",
	"additionalProperties", "additionalPropertiesSchema", "ref", "defs",
}

// schemaInt64Fields 为 Schema 中的 int64 字段，protojson 写成字符串
var schemaInt64Fields = map[string]bool{
	"minItems": true, "maxItems": true, "minProperties": true, "maxProperties": true, "minLength": true, "maxLength": true,
}

// schemaNodeFields、schemaListFields 与 schemaMapFields 为值是单个子 Schema、子 Schema 数组与名称到子 Schema 映射的字段
var (
	schemaNodeFields = map[string]bool{"items": true, "not": true, "additionalPropertiesSchema": true}
	schemaListFields = map[string]bool{"prefixItems": true, "oneOf": true, "anyOf": true, "allOf": true}
	schemaMapFields  = map[string]bool{"properties": true, "defs": true}
)

// marshalSchema 按官方 protojson 的写法编码上游 Schema：字段按声明顺序，int64 字段为字符串，属性映射按键名排序，字符串不转义 HTML 字符
func marshalSchema(schema map[string]any) (json.RawMessage, error) {
	var buffer bytes.Buffer
	err := writeSchema(&buffer, schema)
	return buffer.Bytes(), err
}

// writeSchema 写出一个 Schema 节点，声明顺序之外的字段按键名排序排在最后
func writeSchema(buffer *bytes.Buffer, schema map[string]any) error {
	keys := make([]string, 0, len(schema))
	for _, key := range schemaFieldOrder {
		if _, ok := schema[key]; ok {
			keys = append(keys, key)
		}
	}
	var rest []string
	for key := range schema {
		if !slices.Contains(schemaFieldOrder, key) {
			rest = append(rest, key)
		}
	}
	slices.Sort(rest)
	buffer.WriteByte('{')
	for index, key := range append(keys, rest...) {
		if index > 0 {
			buffer.WriteByte(',')
		}
		if err := writeJSONValue(buffer, key); err != nil {
			return err
		}
		buffer.WriteByte(':')
		value := schema[key]
		var err error
		switch {
		case schemaInt64Fields[key]:
			err = writeInt64String(buffer, value)
		case schemaNodeFields[key]:
			err = writeSchemaNode(buffer, value)
		case schemaListFields[key]:
			err = writeSchemaList(buffer, value)
		case schemaMapFields[key]:
			err = writeSchemaMap(buffer, value)
		default:
			err = writeJSONValue(buffer, value)
		}
		if err != nil {
			return err
		}
	}
	buffer.WriteByte('}')
	return nil
}

// writeSchemaNode 写出一个子 Schema，不是对象时按原值写出
func writeSchemaNode(buffer *bytes.Buffer, value any) error {
	if schema, ok := value.(map[string]any); ok {
		return writeSchema(buffer, schema)
	}
	return writeJSONValue(buffer, value)
}

// writeSchemaList 写出子 Schema 数组
func writeSchemaList(buffer *bytes.Buffer, value any) error {
	items, ok := value.([]any)
	if !ok {
		return writeJSONValue(buffer, value)
	}
	buffer.WriteByte('[')
	for index, item := range items {
		if index > 0 {
			buffer.WriteByte(',')
		}
		if err := writeSchemaNode(buffer, item); err != nil {
			return err
		}
	}
	buffer.WriteByte(']')
	return nil
}

// writeSchemaMap 写出按名称排序的名称到子 Schema 映射
func writeSchemaMap(buffer *bytes.Buffer, value any) error {
	entries, ok := value.(map[string]any)
	if !ok {
		return writeJSONValue(buffer, value)
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	buffer.WriteByte('{')
	for index, name := range names {
		if index > 0 {
			buffer.WriteByte(',')
		}
		if err := writeJSONValue(buffer, name); err != nil {
			return err
		}
		buffer.WriteByte(':')
		if err := writeSchemaNode(buffer, entries[name]); err != nil {
			return err
		}
	}
	buffer.WriteByte('}')
	return nil
}

// writeInt64String 把整数写成 protojson 的 int64 字符串
func writeInt64String(buffer *bytes.Buffer, value any) error {
	switch number := value.(type) {
	case int64:
		return writeJSONValue(buffer, strconv.FormatInt(number, 10))
	case int:
		return writeJSONValue(buffer, strconv.Itoa(number))
	case json.Number:
		return writeJSONValue(buffer, number.String())
	case float64:
		return writeJSONValue(buffer, strconv.FormatInt(int64(number), 10))
	}
	return writeJSONValue(buffer, value)
}

// writeJSONValue 以不转义 HTML 字符的 JSON 写出一个值
func writeJSONValue(buffer *bytes.Buffer, value any) error {
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	buffer.Truncate(buffer.Len() - 1)
	return nil
}
