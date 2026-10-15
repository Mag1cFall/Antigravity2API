package antigravity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// schemaArgumentsNote 为附加在函数描述末尾的完整参数 Schema 前缀
const schemaArgumentsNote = "\nArguments must follow this JSON Schema: "

// schemaTypes 为 JSON Schema 类型名对应的上游 Schema 类型
var schemaTypes = map[string]string{
	"string":  "STRING",
	"number":  "NUMBER",
	"integer": "INTEGER",
	"boolean": "BOOLEAN",
	"array":   "ARRAY",
	"object":  "OBJECT",
}

// schemaFormats 为上游 Schema 各类型接受的 format
var schemaFormats = map[string][]string{
	"STRING":  {"enum", "date-time"},
	"NUMBER":  {"float", "double"},
	"INTEGER": {"int32", "int64"},
}

// schemaLossyFields 为上游 Schema 无法原样表达、出现时需在描述中附完整 Schema 的字段
var schemaLossyFields = []string{"additionalProperties", "prefixItems", "exclusiveMinimum", "propertyNames", "oneOf", "allOf", "not"}

// CleanFunctionDeclaration 把函数声明的 JSON Schema 投影为上游 parameters 接受的 Schema 子集并在 Schema 保留原始 JSON Schema，无法原样表达时在描述末尾附完整 JSON Schema
func CleanFunctionDeclaration(declaration FunctionDeclaration) (FunctionDeclaration, error) {
	if declaration.Name == "" {
		return FunctionDeclaration{}, fmt.Errorf("function declaration 缺少名称")
	}
	raw, err := normalizeFunctionParameters(declaration.Parameters)
	if err != nil {
		return FunctionDeclaration{}, fmt.Errorf("parameters: %w", err)
	}
	resolved, unresolved, err := resolveSchemaRefs(raw)
	if err != nil {
		return FunctionDeclaration{}, fmt.Errorf("parameters: %w", err)
	}
	projector := &schemaProjector{lossy: unresolved}
	parameters, encodingError := projector.project(resolved)
	if encodingError != nil {
		var schema any
		if err := json.Unmarshal(resolved, &schema); err != nil {
			return FunctionDeclaration{}, err
		}
		shape, err := json.Marshal(toolSchemaShape(schema))
		if err != nil {
			return FunctionDeclaration{}, err
		}
		if parameters, err = (&schemaProjector{}).project(shape); err != nil {
			return FunctionDeclaration{}, fmt.Errorf("parameters: %w", err)
		}
	}
	if encodingError != nil || projector.lossy || schemaNeedsType(parameters) || declaration.Strict {
		declaration.Description += schemaArgumentsNote + string(raw)
	}
	fillSchemaTypes(parameters)
	declaration.Schema = raw
	if declaration.Parameters, err = json.Marshal(parameters); err != nil {
		return FunctionDeclaration{}, err
	}
	return declaration, nil
}

// schemaProjector 把 JSON Schema 逐层投影为上游 Schema，并记录是否丢弃了约束
type schemaProjector struct {
	lossy bool
}

// project 校验并投影一个 Schema 节点，无类型节点不写 type
func (p *schemaProjector) project(raw json.RawMessage) (map[string]any, error) {
	raw, err := cleanSchemaInput(raw, false)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(raw, []byte("true")) {
		return map[string]any{}, nil
	}
	if bytes.Equal(raw, []byte("false")) {
		return nil, fmt.Errorf("schema false 禁止所有值")
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return nil, fmt.Errorf("schema 必须是 JSON object")
	}
	if err := normalizeConstAndMetadata(schema); err != nil {
		return nil, err
	}
	if err := normalizeNullableVariants(schema); err != nil {
		return nil, err
	}
	if err := normalizeNotSchema(schema); err != nil {
		return nil, err
	}
	if err := p.projectPrefixItems(schema); err != nil {
		return nil, err
	}
	if raw, ok := schema["items"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
		if minimum, ok := schema["minItems"]; ok {
			value, err := schemaInteger(minimum, "minItems")
			if err != nil || value > 0 {
				return nil, fmt.Errorf("schema.items=false 与 minItems 不兼容")
			}
		}
		schema["items"], schema["maxItems"] = json.RawMessage("true"), json.RawMessage("0")
	}
	if err := normalizeImplicitType(schema); err != nil {
		return nil, err
	}
	allowed := map[string]bool{
		"type": true, "format": true, "description": true, "nullable": true,
		"enum": true, "items": true, "properties": true, "required": true,
		"minItems": true, "maxItems": true, "minProperties": true, "maxProperties": true,
		"minimum": true, "maximum": true, "minLength": true, "maxLength": true,
		"pattern": true, "example": true, "oneOf": true, "anyOf": true,
		"allOf": true, "not": true, "propertyOrdering": true,
		"$schema": true, "additionalProperties": true, "default": true, "exclusiveMinimum": true,
		"propertyNames": true, "prefixItems": true,
	}
	for name := range schema {
		if !allowed[name] {
			return nil, fmt.Errorf("schema 不支持字段 %s", name)
		}
		if slices.Contains(schemaLossyFields, name) {
			p.lossy = true
		}
	}
	typeName, err := schemaType(schema)
	if err != nil {
		return nil, err
	}
	output := make(map[string]any)
	if typeName != "" {
		upstreamType, ok := schemaTypes[strings.ToLower(typeName)]
		if !ok {
			return nil, fmt.Errorf("未知 schema.type %q", typeName)
		}
		output["type"] = upstreamType
	}
	if value, ok := schema["format"]; ok {
		format, err := schemaString(value, "format")
		if err != nil {
			return nil, err
		}
		if upstreamType, _ := output["type"].(string); slices.Contains(schemaFormats[upstreamType], format) {
			output["format"] = format
		} else {
			p.lossy = true
		}
	}
	if value, ok := schema["description"]; ok {
		description, err := schemaString(value, "description")
		if err != nil {
			return nil, err
		}
		output["description"] = description
	}
	if value, ok := schema["nullable"]; ok {
		var nullable bool
		if err := json.Unmarshal(value, &nullable); err != nil {
			return nil, fmt.Errorf("schema.nullable 必须是布尔值")
		}
		output["nullable"] = nullable
	}
	for _, name := range []string{"enum", "required", "propertyOrdering"} {
		if value, ok := schema[name]; ok {
			values, err := schemaStrings(value, name)
			if err != nil && name == "enum" {
				p.lossy = true
				continue
			}
			if err != nil {
				return nil, err
			}
			output[name] = values
		}
	}
	if value, ok := schema["items"]; ok {
		items, err := p.project(value)
		if err != nil {
			return nil, fmt.Errorf("schema.items: %w", err)
		}
		output["items"] = items
	}
	for _, name := range []string{"minItems", "maxItems", "minProperties", "maxProperties", "minLength", "maxLength"} {
		if value, ok := schema[name]; ok {
			integer, err := schemaInteger(value, name)
			if err != nil {
				return nil, err
			}
			output[name] = integer
		}
	}
	if value, ok := schema["properties"]; ok {
		var properties map[string]json.RawMessage
		if err := json.Unmarshal(value, &properties); err != nil || properties == nil {
			return nil, fmt.Errorf("schema.properties 必须是 JSON object")
		}
		projected := make(map[string]any, len(properties))
		for name, property := range properties {
			if projected[name], err = p.project(property); err != nil {
				return nil, fmt.Errorf("schema.properties.%s: %w", name, err)
			}
		}
		output["properties"] = projected
	}
	for _, name := range []string{"minimum", "maximum"} {
		if value, ok := schema[name]; ok {
			number, err := schemaNumber(value, name)
			if err != nil {
				return nil, err
			}
			output[name] = number
		}
	}
	if value, ok := schema["pattern"]; ok {
		pattern, err := schemaString(value, "pattern")
		if err != nil {
			return nil, err
		}
		output["pattern"] = pattern
	}
	if value, ok := schema["example"]; ok {
		var example any
		if err := json.Unmarshal(value, &example); err != nil {
			return nil, fmt.Errorf("schema.example 必须是 JSON value")
		}
		output["example"] = example
	}
	var anyOf []any
	for _, name := range []string{"anyOf", "oneOf"} {
		if value, ok := schema[name]; ok {
			variants, err := p.variants(value, name)
			if err != nil {
				return nil, err
			}
			anyOf = append(anyOf, variants...)
		}
	}
	if len(anyOf) > 0 {
		output["anyOf"] = anyOf
	}
	if value, ok := schema["allOf"]; ok {
		variants, err := p.variants(value, "allOf")
		if err != nil {
			return nil, err
		}
		for _, variant := range variants {
			mergeSchema(output, variant.(map[string]any))
		}
	}
	if output["type"] == "OBJECT" && output["properties"] == nil {
		output["properties"] = map[string]any{}
	}
	return output, nil
}

// projectPrefixItems 把 prefixItems 的各位置 Schema 与 items 合为一个元素 Schema，items=false 时以位置数为 maxItems
func (p *schemaProjector) projectPrefixItems(schema map[string]json.RawMessage) error {
	raw, ok := schema["prefixItems"]
	if !ok {
		return nil
	}
	delete(schema, "prefixItems")
	var variants []json.RawMessage
	if err := json.Unmarshal(raw, &variants); err != nil {
		return fmt.Errorf("schema.prefixItems 必须是 Schema 数组")
	}
	if len(variants) == 0 {
		return nil
	}
	p.lossy = true
	closed := bytes.Equal(bytes.TrimSpace(schema["items"]), []byte("false"))
	if items, ok := schema["items"]; ok && !closed {
		variants = append(variants, items)
	}
	if closed {
		limit := int64(len(variants))
		if value, ok := schema["maxItems"]; ok {
			maximum, err := schemaInteger(value, "maxItems")
			if err != nil {
				return err
			}
			limit = min(limit, maximum)
		}
		schema["maxItems"] = json.RawMessage(strconv.FormatInt(limit, 10))
	}
	distinct := slices.CompactFunc(slices.Clone(variants), func(a, b json.RawMessage) bool { return bytes.Equal(a, b) })
	if len(distinct) == 1 {
		schema["items"] = distinct[0]
		return nil
	}
	items, err := json.Marshal(map[string][]json.RawMessage{"anyOf": distinct})
	if err != nil {
		return err
	}
	schema["items"] = items
	return nil
}

// resolveSchemaRefs 按根节点的 $defs 与 definitions 展开本地 $ref，递归引用只保留类型，无法解析的引用展开为不限制的 Schema，两者出现时返回 true
func resolveSchemaRefs(raw json.RawMessage) (json.RawMessage, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, false, err
	}
	object, ok := root.(map[string]any)
	if !ok {
		return raw, false, nil
	}
	resolver := &schemaRefResolver{definitions: map[string]any{}}
	for _, key := range []string{"$defs", "definitions"} {
		if definitions, ok := object[key].(map[string]any); ok {
			for name, definition := range definitions {
				resolver.definitions["#/"+key+"/"+name] = definition
			}
		}
	}
	resolver.definitions["#"] = root
	resolved, err := json.Marshal(resolver.resolve(root, nil))
	return resolved, resolver.unresolved, err
}

// schemaRefResolver 展开本地 $ref
type schemaRefResolver struct {
	definitions map[string]any
	unresolved  bool
}

// resolve 展开节点中的 $ref 并去掉 $defs 与 definitions，stack 为正在展开的引用
func (r *schemaRefResolver) resolve(value any, stack []string) any {
	switch value := value.(type) {
	case []any:
		resolved := make([]any, len(value))
		for index, item := range value {
			resolved[index] = r.resolve(item, stack)
		}
		return resolved
	case map[string]any:
		if reference, ok := value["$ref"].(string); ok {
			definition, found := r.definitions[reference]
			if !found {
				r.unresolved = true
				return true
			}
			if slices.Contains(stack, reference) {
				r.unresolved = true
				if object, ok := definition.(map[string]any); ok && object["type"] != nil {
					return map[string]any{"type": object["type"]}
				}
				return true
			}
			merged := map[string]any{}
			if object, ok := definition.(map[string]any); ok {
				for key, item := range object {
					merged[key] = item
				}
			} else {
				return r.resolve(definition, append(stack, reference))
			}
			for key, item := range value {
				if key != "$ref" {
					merged[key] = item
				}
			}
			return r.resolve(merged, append(stack, reference))
		}
		resolved := make(map[string]any, len(value))
		for key, item := range value {
			if key == "$defs" || key == "definitions" {
				continue
			}
			if key == "properties" {
				if properties, ok := item.(map[string]any); ok {
					children := make(map[string]any, len(properties))
					for name, property := range properties {
						children[name] = r.resolve(property, stack)
					}
					resolved[key] = children
					continue
				}
			}
			resolved[key] = r.resolve(item, stack)
		}
		return resolved
	}
	return value
}

// variants 投影 anyOf、oneOf 或 allOf 的各分支
func (p *schemaProjector) variants(raw json.RawMessage, name string) ([]any, error) {
	var variants []json.RawMessage
	if err := json.Unmarshal(raw, &variants); err != nil {
		return nil, fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
	}
	projected := make([]any, 0, len(variants))
	for index, variant := range variants {
		schema, err := p.project(variant)
		if err != nil {
			return nil, fmt.Errorf("schema.%s[%d]: %w", name, index, err)
		}
		projected = append(projected, schema)
	}
	return projected, nil
}

// mergeSchema 把 allOf 分支并入节点，节点自身字段优先，properties 与 required 取并集
func mergeSchema(target map[string]any, source map[string]any) {
	for key, value := range source {
		switch key {
		case "properties":
			properties, _ := target["properties"].(map[string]any)
			if properties == nil {
				properties = make(map[string]any)
				target["properties"] = properties
			}
			for name, property := range value.(map[string]any) {
				if _, exists := properties[name]; !exists {
					properties[name] = property
				}
			}
		case "required":
			required, _ := target["required"].([]string)
			for _, name := range value.([]string) {
				if !slices.Contains(required, name) {
					required = append(required, name)
				}
			}
			target["required"] = required
		default:
			if _, exists := target[key]; !exists {
				target[key] = value
			}
		}
	}
}

// schemaNeedsType 查找没有类型的节点
func schemaNeedsType(schema map[string]any) bool {
	if schema["type"] == nil {
		return true
	}
	if items, ok := schema["items"].(map[string]any); ok && schemaNeedsType(items) {
		return true
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, property := range properties {
			if schemaNeedsType(property.(map[string]any)) {
				return true
			}
		}
	}
	if variants, ok := schema["anyOf"].([]any); ok {
		for _, variant := range variants {
			if schemaNeedsType(variant.(map[string]any)) {
				return true
			}
		}
	}
	return false
}

// fillSchemaTypes 为无类型的联合节点补上首个分支的类型，没有分支的无类型节点保持不限类型
func fillSchemaTypes(schema map[string]any) {
	if items, ok := schema["items"].(map[string]any); ok {
		fillSchemaTypes(items)
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, property := range properties {
			fillSchemaTypes(property.(map[string]any))
		}
	}
	if variants, ok := schema["anyOf"].([]any); ok {
		for _, variant := range variants {
			fillSchemaTypes(variant.(map[string]any))
		}
		if schema["type"] == nil && len(variants) > 0 {
			first := variants[0].(map[string]any)
			schema["type"] = first["type"]
			if first["type"] == "ARRAY" && schema["items"] == nil && first["items"] != nil {
				schema["items"] = first["items"]
			}
		}
	}
}

// claudeParameters 把上游 Schema 中的 anyOf 与可空标量改写为 Claude 族接受的形状，返回改写后的 Schema 与是否丢弃了约束，两者都不含时原样返回
func claudeParameters(raw json.RawMessage) (json.RawMessage, bool, error) {
	if !bytes.Contains(raw, []byte(`"anyOf"`)) && !bytes.Contains(raw, []byte(`"nullable":true`)) {
		return raw, false, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false, err
	}
	unions := flattenUnions(schema)
	nullable := untypeNullable(schema)
	encoded, err := json.Marshal(schema)
	return encoded, unions || nullable, err
}

// untypeNullable 逐层把可空的标量节点改为只留描述的无类型节点，Claude 族上游丢弃 nullable，带类型时模型无法给出 null；改写过时返回 true
func untypeNullable(schema map[string]any) bool {
	lossy := false
	if items, ok := schema["items"].(map[string]any); ok {
		lossy = untypeNullable(items) || lossy
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, property := range properties {
			lossy = untypeNullable(property.(map[string]any)) || lossy
		}
	}
	if schema["nullable"] != true || schema["type"] == "OBJECT" || schema["type"] == "ARRAY" {
		return lossy
	}
	for key := range schema {
		if key != "description" {
			delete(schema, key)
		}
	}
	return true
}

// flattenUnions 逐层去掉 anyOf：只有一个分支时以该分支取代联合，分支都是 OBJECT 时合并为一个 OBJECT，其余只保留描述与 nullable，后两种返回 true
func flattenUnions(schema map[string]any) bool {
	lossy := false
	if items, ok := schema["items"].(map[string]any); ok {
		lossy = flattenUnions(items) || lossy
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, property := range properties {
			lossy = flattenUnions(property.(map[string]any)) || lossy
		}
	}
	variants, ok := schema["anyOf"].([]any)
	if !ok {
		return lossy
	}
	delete(schema, "anyOf")
	objects := true
	for _, variant := range variants {
		lossy = flattenUnions(variant.(map[string]any)) || lossy
		objects = objects && variant.(map[string]any)["type"] == "OBJECT"
	}
	if len(variants) == 1 {
		for key, value := range variants[0].(map[string]any) {
			if _, own := schema[key]; !own || key == "type" || key == "items" || key == "properties" {
				schema[key] = value
			}
		}
		return lossy
	}
	for key := range schema {
		if key != "description" && key != "nullable" {
			delete(schema, key)
		}
	}
	if objects {
		properties := map[string]any{}
		var required []string
		for index, variant := range variants {
			object := variant.(map[string]any)
			for name, property := range object["properties"].(map[string]any) {
				if _, exists := properties[name]; !exists {
					properties[name] = property
				}
			}
			names, _ := object["required"].([]any)
			if index == 0 {
				for _, name := range names {
					required = append(required, name.(string))
				}
				continue
			}
			required = slices.DeleteFunc(required, func(name string) bool { return !slices.Contains(names, any(name)) })
		}
		schema["type"], schema["properties"] = "OBJECT", properties
		if len(required) > 0 {
			schema["required"] = required
		}
	}
	return true
}

// normalizeFunctionParameters 将零参数定义归一化为对象
func normalizeFunctionParameters(raw json.RawMessage) (json.RawMessage, error) {
	cleaned, err := cleanSchemaInput(raw, true)
	if err == nil && (bytes.Equal(cleaned, []byte("{}")) || bytes.Equal(cleaned, []byte("true"))) {
		cleaned = json.RawMessage(`{"type":"object"}`)
	}
	return cleaned, err
}

// cleanSchemaInput 清理可选空值并按工具与输出契约保留组合节点
func cleanSchemaInput(raw json.RawMessage, preserveCombinations bool) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return json.RawMessage(`{"type":"object"}`), nil
	}
	if bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false")) {
		return raw, nil
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(raw, &schema) != nil || schema == nil {
		return nil, fmt.Errorf("schema 必须是 JSON object 或 boolean")
	}
	for _, name := range []string{"type", "format", "description", "nullable", "enum", "items", "properties", "required", "minItems", "maxItems", "minProperties", "maxProperties", "minimum", "maximum", "minLength", "maxLength", "pattern", "oneOf", "anyOf", "allOf", "not", "propertyOrdering"} {
		if bytes.Equal(bytes.TrimSpace(schema[name]), []byte("null")) {
			delete(schema, name)
		}
	}
	var typeName string
	if json.Unmarshal(schema["type"], &typeName) == nil && strings.EqualFold(typeName, "TYPE_UNSPECIFIED") {
		delete(schema, "type")
	}
	for _, name := range []string{"minItems", "maxItems", "minProperties", "maxProperties", "minLength", "maxLength"} {
		if value, ok := schema[name]; ok {
			schema[name] = canonicalSchemaInteger(value)
		}
	}
	for _, name := range []string{"items", "not"} {
		if nested := bytes.TrimSpace(schema[name]); len(nested) > 0 && nested[0] == '{' {
			cleaned, err := cleanSchemaInput(nested, preserveCombinations)
			if err != nil {
				return nil, fmt.Errorf("schema.%s: %w", name, err)
			}
			schema[name] = cleaned
		}
	}
	if value, ok := schema["properties"]; ok {
		var properties map[string]json.RawMessage
		if json.Unmarshal(value, &properties) != nil || properties == nil {
			return nil, fmt.Errorf("schema.properties 必须是 JSON object")
		}
		for name, property := range properties {
			if bytes.Equal(bytes.TrimSpace(property), []byte("null")) {
				property = json.RawMessage("true")
			}
			cleaned, err := cleanSchemaInput(property, preserveCombinations)
			if err != nil {
				return nil, fmt.Errorf("schema.properties.%s: %w", name, err)
			}
			properties[name] = cleaned
		}
		schema["properties"], _ = json.Marshal(properties)
	}
	for _, name := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if raw, ok := schema[name]; ok {
			var variants []json.RawMessage
			if json.Unmarshal(raw, &variants) != nil {
				return nil, fmt.Errorf("schema.%s 必须是 Schema 数组", name)
			}
			filtered := make([]json.RawMessage, 0, len(variants))
			unrestricted := false
			for index, variant := range variants {
				cleaned, err := cleanSchemaInput(variant, preserveCombinations)
				if err != nil {
					return nil, fmt.Errorf("schema.%s[%d]: %w", name, index, err)
				}
				if preserveCombinations {
					filtered = append(filtered, cleaned)
					continue
				}
				if name == "anyOf" && bytes.Equal(cleaned, []byte("true")) {
					unrestricted = true
					break
				}
				if name == "allOf" && bytes.Equal(cleaned, []byte("false")) {
					return nil, fmt.Errorf("schema.allOf 禁止所有值")
				}
				if (name == "anyOf" || name == "oneOf") && bytes.Equal(cleaned, []byte("false")) || name == "allOf" && bytes.Equal(cleaned, []byte("true")) {
					continue
				}
				filtered = append(filtered, cleaned)
			}
			if unrestricted || name == "allOf" && len(filtered) == 0 {
				delete(schema, name)
			} else if len(filtered) == 0 && (name == "anyOf" || name == "oneOf") {
				return nil, fmt.Errorf("schema.%s 禁止所有值", name)
			} else {
				schema[name], _ = json.Marshal(filtered)
			}
		}
	}
	return json.Marshal(schema)
}

// normalizeNullableVariants 将 JSON Schema null 联合映射为 nullable
func normalizeNullableVariants(schema map[string]json.RawMessage) error {
	if raw, ok := schema["type"]; ok {
		var typeName string
		if err := json.Unmarshal(raw, &typeName); err != nil {
			var typeNames []string
			if arrayErr := json.Unmarshal(raw, &typeNames); arrayErr != nil || len(typeNames) == 0 {
				return fmt.Errorf("schema.type 必须是字符串或字符串数组")
			}
			nonNull := make([]string, 0, len(typeNames))
			nullable := false
			for _, name := range typeNames {
				if strings.EqualFold(name, "null") {
					nullable = true
					continue
				}
				nonNull = append(nonNull, name)
			}
			if len(nonNull) == 0 {
				return fmt.Errorf("schema.type 必须包含非 null 类型")
			}
			encodedType, marshalErr := json.Marshal(nonNull[0])
			if marshalErr != nil {
				return marshalErr
			}
			schema["type"] = encodedType
			if len(nonNull) > 1 {
				delete(schema, "type")
				variants := make([]map[string]string, 0, len(nonNull))
				for _, name := range nonNull {
					variants = append(variants, map[string]string{"type": name})
				}
				encodedVariants, marshalErr := json.Marshal(variants)
				if marshalErr != nil {
					return marshalErr
				}
				schema["anyOf"] = encodedVariants
			}
			if nullable {
				schema["nullable"] = json.RawMessage("true")
			}
		}
	}
	for _, name := range []string{"anyOf", "oneOf"} {
		raw, ok := schema[name]
		if !ok {
			continue
		}
		var variants []json.RawMessage
		if err := json.Unmarshal(raw, &variants); err != nil {
			return fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
		}
		filtered := variants[:0]
		nullable := false
		for _, variant := range variants {
			if bytes.Equal(bytes.TrimSpace(variant), []byte("true")) || bytes.Equal(bytes.TrimSpace(variant), []byte("false")) {
				filtered = append(filtered, variant)
				continue
			}
			var value map[string]json.RawMessage
			if err := json.Unmarshal(variant, &value); err != nil || value == nil {
				return fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
			}
			typeValue, exists := value["type"]
			if exists {
				typeName, err := schemaString(typeValue, "type")
				if err != nil {
					return err
				}
				if strings.EqualFold(typeName, "null") {
					nullable = true
					continue
				}
			}
			filtered = append(filtered, variant)
		}
		if !nullable {
			continue
		}
		if len(filtered) == 0 {
			return fmt.Errorf("schema.%s 必须包含非 null 类型", name)
		}
		encoded, err := json.Marshal(filtered)
		if err != nil {
			return err
		}
		schema[name] = encoded
		schema["nullable"] = json.RawMessage("true")
	}
	return nil
}

// normalizeConstAndMetadata 将字符串常量和说明字段转换为可发送结构
func normalizeConstAndMetadata(schema map[string]json.RawMessage) error {
	delete(schema, "title")
	delete(schema, "$id")
	delete(schema, "$comment")
	if raw, ok := schema["const"]; ok {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return fmt.Errorf("schema.const 必须是字符串")
		}
		value, ok := decoded.(string)
		if !ok {
			return fmt.Errorf("schema.const 只支持字符串")
		}
		if rawType, exists := schema["type"]; exists {
			typeName, err := schemaString(rawType, "type")
			if err != nil || !strings.EqualFold(typeName, "string") {
				return fmt.Errorf("schema.const 只支持 string 类型")
			}
		} else {
			schema["type"] = json.RawMessage(`"string"`)
		}
		enum, err := json.Marshal([]string{value})
		if err != nil {
			return err
		}
		schema["enum"] = enum
		delete(schema, "const")
	}
	for _, name := range []string{"anyOf", "oneOf", "allOf"} {
		raw, ok := schema[name]
		if !ok {
			continue
		}
		var variants []json.RawMessage
		if err := json.Unmarshal(raw, &variants); err != nil {
			return fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
		}
		for index, variant := range variants {
			if bytes.Equal(bytes.TrimSpace(variant), []byte("true")) || bytes.Equal(bytes.TrimSpace(variant), []byte("false")) {
				continue
			}
			var subSchema map[string]json.RawMessage
			if err := json.Unmarshal(variant, &subSchema); err != nil || subSchema == nil {
				return fmt.Errorf("schema.%s 必须是 JSON object 数组", name)
			}
			if err := normalizeConstAndMetadata(subSchema); err != nil {
				return fmt.Errorf("schema.%s[%d]: %w", name, index, err)
			}
			encoded, err := json.Marshal(subSchema)
			if err != nil {
				return err
			}
			variants[index] = encoded
		}
		encoded, err := json.Marshal(variants)
		if err != nil {
			return err
		}
		schema[name] = encoded
	}
	return nil
}

// normalizeNotSchema 保留排除值并处理不接受任何值的约束
func normalizeNotSchema(schema map[string]json.RawMessage) error {
	raw, ok := schema["not"]
	if !ok {
		return nil
	}
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("false")) {
		delete(schema, "not")
		return nil
	}
	if bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("{}")) {
		return fmt.Errorf("schema.not 禁止所有值")
	}
	var sub map[string]json.RawMessage
	if json.Unmarshal(raw, &sub) == nil && sub != nil {
		var typeName string
		if len(sub) == 1 && json.Unmarshal(sub["type"], &typeName) == nil && strings.EqualFold(typeName, "null") {
			delete(schema, "not")
			schema["nullable"] = json.RawMessage("false")
		}
		return nil
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("schema.not 必须是 Schema 或字符串排除值")
		}
		values = []string{value}
	}
	if len(values) == 0 {
		delete(schema, "not")
		return nil
	}
	schema["not"], _ = json.Marshal(map[string]any{"type": "string", "enum": values})
	return nil
}

// normalizeImplicitType 从节点约束推导类型并保留开放的数组元素
func normalizeImplicitType(schema map[string]json.RawMessage) error {
	if _, ok := schema["type"]; !ok {
		var typed map[string]json.RawMessage
		conflictingType, conflictingItems := false, false
		for _, name := range []string{"anyOf", "oneOf", "allOf"} {
			var variants []map[string]json.RawMessage
			if raw, ok := schema[name]; ok && json.Unmarshal(raw, &variants) == nil {
				for _, variant := range variants {
					if value, ok := variant["type"]; ok {
						if typed == nil {
							typed = variant
						} else {
							conflictingType = conflictingType || !bytes.Equal(bytes.TrimSpace(value), bytes.TrimSpace(typed["type"]))
							conflictingItems = conflictingItems || !bytes.Equal(bytes.TrimSpace(variant["items"]), bytes.TrimSpace(typed["items"]))
						}
					}
				}
			}
		}
		switch {
		case schema["properties"] != nil:
			schema["type"] = json.RawMessage(`"object"`)
		case schema["items"] != nil || schema["prefixItems"] != nil:
			schema["type"] = json.RawMessage(`"array"`)
		default:
			if schema["enum"] != nil || schema["pattern"] != nil || schema["format"] != nil || schema["minLength"] != nil || schema["maxLength"] != nil {
				schema["type"] = json.RawMessage(`"string"`)
			}
		}
		if typed != nil && !conflictingType {
			schema["type"] = typed["type"]
			if items, ok := typed["items"]; ok && schema["items"] == nil && !conflictingItems {
				schema["items"] = items
			}
		}
	}
	var typeName string
	if json.Unmarshal(schema["type"], &typeName) != nil || !strings.EqualFold(typeName, "array") || schema["items"] != nil {
		return nil
	}
	schema["items"] = json.RawMessage(`true`)
	var prefix []map[string]json.RawMessage
	if raw, ok := schema["prefixItems"]; ok && json.Unmarshal(raw, &prefix) == nil {
		typed := make([]map[string]json.RawMessage, 0, len(prefix))
		for _, item := range prefix {
			if _, ok := item["type"]; ok {
				typed = append(typed, item)
			}
		}
		if len(typed) > 0 {
			encoded, err := json.Marshal(map[string]any{"anyOf": typed})
			if err != nil {
				return err
			}
			schema["items"] = encoded
		}
	}
	return nil
}

// toolSchemaShape 提取无法直接投影的工具参数的层级与类型
func toolSchemaShape(value any) map[string]any {
	result := map[string]any{}
	object, ok := value.(map[string]any)
	if !ok {
		return result
	}
	for _, key := range []string{"type", "format", "nullable", "description", "required", "minimum", "maximum", "minLength", "maxLength", "pattern", "minItems", "maxItems", "minProperties", "maxProperties", "propertyOrdering"} {
		if item, exists := object[key]; exists {
			result[key] = item
		}
	}
	if constant, exists := object["const"]; exists && constant != nil {
		encoded, _ := json.Marshal(constant)
		description, _ := result["description"].(string)
		result["description"] = description + "\nThe value must be: " + string(encoded)
		switch value := constant.(type) {
		case float64:
			result["type"] = "number"
			if math.Trunc(value) == value {
				result["type"] = "integer"
			}
			result["minimum"], result["maximum"] = value, value
		case bool:
			result["type"] = "boolean"
		case string:
			result["type"], result["enum"] = "string", []any{value}
		case []any:
			result["type"] = "array"
		case map[string]any:
			result["type"] = "object"
		}
	}
	if values, ok := object["enum"].([]any); ok && len(values) > 0 {
		stringsOnly := true
		for _, value := range values {
			_, isString := value.(string)
			stringsOnly = stringsOnly && isString
		}
		if stringsOnly {
			result["enum"] = values
		}
	}
	if typeName, ok := result["type"].(string); ok && strings.EqualFold(typeName, "null") {
		result["type"], result["nullable"] = "string", true
		result["description"] = "The value must be JSON null"
	}
	if types, ok := result["type"].([]any); ok && len(types) > 0 {
		nullOnly := true
		for _, name := range types {
			nullOnly = nullOnly && strings.EqualFold(fmt.Sprint(name), "null")
		}
		if nullOnly {
			result["type"], result["nullable"] = "string", true
			result["description"] = "The value must be JSON null"
		}
	}
	if constant, exists := object["const"]; exists && constant == nil {
		result["type"], result["nullable"] = "string", true
		result["description"] = "The value must be JSON null"
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		children := map[string]any{}
		for key, child := range properties {
			children[key] = toolSchemaShape(child)
		}
		result["properties"] = children
	}
	if child, exists := object["items"]; exists {
		result["items"] = toolSchemaShape(child)
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if branches, ok := object[key].([]any); ok {
			children := make([]any, len(branches))
			for index, branch := range branches {
				children[index] = toolSchemaShape(branch)
			}
			result[key] = children
		}
	}
	return result
}

func schemaType(schema map[string]json.RawMessage) (string, error) {
	if value, ok := schema["type"]; ok {
		typeName, err := schemaString(value, "type")
		if err != nil || typeName == "" {
			return "", fmt.Errorf("schema.type 必须是字符串")
		}
		return typeName, nil
	}
	return "", nil
}

// canonicalSchemaInteger 把 Gemini Schema 的字符串 int64 与零小数数字改写为整数字面量，其他值保持原样
func canonicalSchemaInteger(raw json.RawMessage) json.RawMessage {
	value := string(bytes.TrimSpace(raw))
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value = text
	}
	if integer, err := strconv.ParseInt(value, 10, 64); err == nil {
		return json.RawMessage(strconv.FormatInt(integer, 10))
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number != math.Trunc(number) || math.Abs(number) > 1<<53 {
		return raw
	}
	return json.RawMessage(strconv.FormatInt(int64(number), 10))
}

func schemaInteger(raw json.RawMessage, name string) (int64, error) {
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("schema.%s 必须是非负整数", name)
	}
	return value, nil
}

func schemaNumber(raw json.RawMessage, name string) (float64, error) {
	value, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("schema.%s 必须是数字", name)
	}
	return value, nil
}

func schemaString(raw json.RawMessage, name string) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("schema.%s 必须是字符串", name)
	}
	return value, nil
}

func schemaStrings(raw json.RawMessage, name string) ([]string, error) {
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("schema.%s 必须是字符串数组", name)
	}
	return values, nil
}
