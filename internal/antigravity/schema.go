package antigravity

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// claudePropertyKey 为 Claude 族工具 Schema 接受的属性键模式
var claudePropertyKey = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

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
var schemaLossyFields = []string{"additionalProperties", "additionalItems", "prefixItems", "exclusiveMinimum", "propertyNames", "oneOf", "allOf", "not"}

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
		_ = json.Unmarshal(resolved, &schema)
		shape, _ := json.Marshal(toolSchemaShape(schema))
		if parameters, err = (&schemaProjector{}).project(shape); err != nil {
			return FunctionDeclaration{}, fmt.Errorf("parameters: %w", err)
		}
	}
	if encodingError != nil || projector.lossy || schemaNeedsType(parameters) || declaration.Strict {
		declaration.Description += schemaArgumentsNote + string(raw)
	}
	fillSchemaTypes(parameters)
	declaration.Schema = raw
	if declaration.Parameters, err = marshalSchema(parameters); err != nil {
		return FunctionDeclaration{}, err
	}
	return declaration, nil
}

// schemaProjector 把 JSON Schema 逐层投影为上游 Schema，并记录是否丢弃了约束
type schemaProjector struct {
	lossy bool
}

// project 校验并投影一个 Schema 节点，无类型节点不写 type，enum 去掉上游拒绝的空串
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
	normalizeImplicitType(schema)
	allowed := map[string]bool{
		"type": true, "format": true, "description": true, "nullable": true,
		"enum": true, "items": true, "properties": true, "required": true,
		"minItems": true, "maxItems": true, "minProperties": true, "maxProperties": true,
		"minimum": true, "maximum": true, "minLength": true, "maxLength": true,
		"pattern": true, "example": true, "oneOf": true, "anyOf": true,
		"allOf": true, "not": true, "propertyOrdering": true,
		"$schema": true, "additionalProperties": true, "additionalItems": true, "default": true, "exclusiveMinimum": true,
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
			if name == "enum" && slices.Contains(values, "") {
				values = slices.DeleteFunc(values, func(value string) bool { return value == "" })
				p.lossy = true
				if len(values) == 0 {
					continue
				}
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
	p.dropMismatchedKeywords(output)
	ensureRequiredProperties(output)
	return output, nil
}

// ensureRequiredProperties 为对象节点中 required 列出而 properties 未定义的属性补上不限类型的定义，避免上游拒绝
func ensureRequiredProperties(output map[string]any) {
	required, ok := output["required"].([]string)
	if !ok || len(required) == 0 {
		return
	}
	upstreamType, _ := output["type"].(string)
	if upstreamType != "" && upstreamType != "OBJECT" {
		return
	}
	properties, _ := output["properties"].(map[string]any)
	for _, name := range required {
		if _, defined := properties[name]; defined {
			continue
		}
		if properties == nil {
			properties = make(map[string]any)
			output["properties"] = properties
		}
		properties[name] = map[string]any{}
	}
	if upstreamType == "" && len(properties) > 0 {
		output["type"] = "OBJECT"
	}
}

// dropMismatchedKeywords 去掉与节点类型不符的 items、properties 与 required，上游只在 ARRAY 接受 items、只在 OBJECT 接受后两者
func (p *schemaProjector) dropMismatchedKeywords(output map[string]any) {
	upstreamType, _ := output["type"].(string)
	if upstreamType == "" {
		return
	}
	for name, owner := range map[string]string{"items": "ARRAY", "properties": "OBJECT", "required": "OBJECT"} {
		if _, ok := output[name]; ok && upstreamType != owner {
			delete(output, name)
			p.lossy = true
		}
	}
}

// projectPrefixItems 把 Draft-07 元组 items（含空数组与 additionalItems）和 prefixItems 的各位置 Schema 与 items 合为一个元素 Schema，禁止额外元素时以位置数为 maxItems
func (p *schemaProjector) projectPrefixItems(schema map[string]json.RawMessage) error {
	if raw, ok := schema["items"]; ok {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			var tuple []json.RawMessage
			if err := json.Unmarshal(trimmed, &tuple); err != nil {
				return fmt.Errorf("schema.items 必须是 Schema 或 Schema 数组")
			}
			delete(schema, "items")
			additional, hasAdditional := schema["additionalItems"]
			delete(schema, "additionalItems")
			if len(tuple) == 0 {
				if hasAdditional {
					if bytes.Equal(bytes.TrimSpace(additional), []byte("false")) {
						p.lossy = true
						schema["items"] = json.RawMessage("false")
					} else {
						schema["items"] = additional
					}
				} else {
					schema["items"] = json.RawMessage("true")
				}
			} else if _, hasPrefix := schema["prefixItems"]; !hasPrefix {
				schema["prefixItems"] = trimmed
				if hasAdditional {
					schema["items"] = additional
				}
			} else {
				p.lossy = true
			}
		}
	}
	if _, ok := schema["additionalItems"]; ok {
		delete(schema, "additionalItems")
		p.lossy = true
	}
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
		if _, hasItems := schema["items"]; !hasItems {
			schema["items"] = json.RawMessage("true")
		} else if bytes.Equal(bytes.TrimSpace(schema["items"]), []byte("false")) {
			p.lossy = true
		}
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
	distinct := make([]json.RawMessage, 0, len(variants))
	for _, variant := range variants {
		if !slices.ContainsFunc(distinct, func(existing json.RawMessage) bool { return bytes.Equal(existing, variant) }) {
			distinct = append(distinct, variant)
		}
	}
	if len(distinct) == 1 {
		schema["items"] = distinct[0]
		return nil
	}
	schema["items"], _ = json.Marshal(map[string][]json.RawMessage{"anyOf": distinct})
	return nil
}

// maxSchemaRefExpansions 为一个声明中展开本地 $ref 的总次数上限
const maxSchemaRefExpansions = 500

// resolveSchemaRefs 按根节点的 $defs 与 definitions 展开本地 $ref，递归引用与超过 maxSchemaRefExpansions 之后的引用只保留类型，无法解析的引用展开为不限制的 Schema，三者出现时返回 true
func resolveSchemaRefs(raw json.RawMessage) (json.RawMessage, bool, error) {
	root, err := decodeSchemaValue(raw)
	if err != nil {
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

// schemaRefResolver 展开本地 $ref，expanded 为已展开的次数
type schemaRefResolver struct {
	definitions map[string]any
	unresolved  bool
	expanded    int
}

// schemaAnnotationKeys 为不参与校验约束、允许 $ref 同级直接覆盖的注解字段
var schemaAnnotationKeys = map[string]bool{
	"description": true, "title": true, "default": true, "example": true,
	"examples": true, "$comment": true, "deprecated": true,
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
			siblings := map[string]any{}
			for key, item := range value {
				if key != "$ref" && key != "$defs" && key != "definitions" {
					siblings[key] = item
				}
			}
			definition, found := r.definitions[reference]
			if !found {
				r.unresolved = true
				if len(siblings) == 0 {
					return true
				}
				return r.resolve(siblings, stack)
			}
			if slices.Contains(stack, reference) || r.expanded >= maxSchemaRefExpansions {
				r.unresolved = true
				var fallback any = true
				if object, ok := definition.(map[string]any); ok && object["type"] != nil {
					fallback = map[string]any{"type": object["type"]}
				}
				if len(siblings) == 0 {
					return fallback
				}
				return combineRefSiblings(fallback, r.resolve(siblings, stack))
			}
			r.expanded++
			resolvedTarget := r.resolve(definition, append(stack, reference))
			if len(siblings) == 0 {
				return resolvedTarget
			}
			return combineRefSiblings(resolvedTarget, r.resolve(siblings, stack))
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

// combineRefSiblings 按合取语义合并已展开的 $ref 目标与同级字段：同级只有注解时直接覆盖，含校验约束时以 allOf 保留双方约束
func combineRefSiblings(target any, siblings any) any {
	if target == true {
		return siblings
	}
	if target == false {
		return false
	}
	targetObj, okTarget := target.(map[string]any)
	siblingObj, okSibling := siblings.(map[string]any)
	if !okTarget || !okSibling {
		return target
	}
	annotations := map[string]any{}
	constraints := map[string]any{}
	for key, item := range siblingObj {
		if schemaAnnotationKeys[key] {
			annotations[key] = item
		} else {
			constraints[key] = item
		}
	}
	if len(constraints) == 0 {
		merged := make(map[string]any, len(targetObj)+len(annotations))
		for key, item := range targetObj {
			merged[key] = item
		}
		for key, item := range annotations {
			merged[key] = item
		}
		return merged
	}
	combined := map[string]any{"allOf": []any{targetObj, constraints}}
	for key, item := range annotations {
		combined[key] = item
	}
	return combined
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

// mergeSchema 把 allOf 分支按合取语义并入节点：properties 与 items 递归合并，required 取并集，数值与长度上下界取更严值，enum 取交集
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
				if existing, exists := properties[name].(map[string]any); exists {
					if incoming, ok := property.(map[string]any); ok {
						mergeSchema(existing, incoming)
						continue
					}
				}
				properties[name] = property
			}
		case "items":
			if existing, exists := target["items"].(map[string]any); exists {
				if incoming, ok := value.(map[string]any); ok {
					mergeSchema(existing, incoming)
					continue
				}
			}
			if _, exists := target["items"]; !exists {
				target["items"] = value
			}
		case "required":
			required, _ := target["required"].([]string)
			for _, name := range value.([]string) {
				if !slices.Contains(required, name) {
					required = append(required, name)
				}
			}
			target["required"] = required
		case "minimum":
			if existing, exists := target[key].(float64); exists {
				target[key] = max(existing, value.(float64))
			} else {
				target[key] = value
			}
		case "maximum":
			if existing, exists := target[key].(float64); exists {
				target[key] = min(existing, value.(float64))
			} else {
				target[key] = value
			}
		case "minLength", "minItems", "minProperties":
			if existing, exists := target[key].(int64); exists {
				target[key] = max(existing, value.(int64))
			} else {
				target[key] = value
			}
		case "maxLength", "maxItems", "maxProperties":
			if existing, exists := target[key].(int64); exists {
				target[key] = min(existing, value.(int64))
			} else {
				target[key] = value
			}
		case "enum":
			incoming := value.([]string)
			if existing, exists := target[key].([]string); exists {
				var intersection []string
				for _, item := range existing {
					if slices.Contains(incoming, item) {
						intersection = append(intersection, item)
					}
				}
				if len(intersection) > 0 {
					target[key] = intersection
				}
			} else {
				target[key] = incoming
			}
		case "type":
			if existing, exists := target[key].(string); exists {
				if existing == "NUMBER" && value == "INTEGER" {
					target[key] = "INTEGER"
				}
			} else {
				target[key] = value
			}
		case "nullable":
			if existing, exists := target[key].(bool); exists {
				target[key] = existing && value.(bool)
			} else {
				target[key] = value
			}
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

// claudeParameters 把上游 Schema 中的 anyOf、可空标量与不合规属性键改写为 Claude 族接受的形状，返回改写后的 Schema 与是否丢弃了约束，不含 anyOf、可空标量且没有降级属性键时原样返回
func claudeParameters(raw json.RawMessage) (json.RawMessage, bool, error) {
	if !bytes.Contains(raw, []byte(`"anyOf"`)) && !bytes.Contains(raw, []byte(`"nullable":true`)) && !bytes.Contains(raw, []byte(`"properties"`)) {
		return raw, false, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, false, err
	}
	unions := flattenUnions(schema)
	nullable := untypeNullable(schema)
	keys := dropInvalidClaudePropertyKeys(schema)
	if !keys && !bytes.Contains(raw, []byte(`"anyOf"`)) && !bytes.Contains(raw, []byte(`"nullable":true`)) {
		return raw, false, nil
	}
	encoded, err := json.Marshal(schema)
	return encoded, unions || nullable || keys, err
}

// dropInvalidClaudePropertyKeys 逐层检查 properties 键名是否满足 Claude 族模式，含不合规键的对象退化为不限属性的对象，改写过时返回 true
func dropInvalidClaudePropertyKeys(schema map[string]any) bool {
	lossy := false
	if items, ok := schema["items"].(map[string]any); ok {
		lossy = dropInvalidClaudePropertyKeys(items) || lossy
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return lossy
	}
	for name := range properties {
		if !claudePropertyKey.MatchString(name) {
			delete(schema, "properties")
			delete(schema, "required")
			delete(schema, "propertyOrdering")
			return true
		}
	}
	for _, property := range properties {
		if child, ok := property.(map[string]any); ok {
			lossy = dropInvalidClaudePropertyKeys(child) || lossy
		}
	}
	return lossy
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

// flattenUnions 逐层展开并移除 anyOf，返回是否丢弃了约束
// 单分支取代联合，全为 OBJECT 时合并，其余保留描述与 nullable，见 docs/protocol.md“工具声明”
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
			branch, _ := object["properties"].(map[string]any)
			for name, property := range branch {
				existing, exists := properties[name].(map[string]any)
				if !exists {
					properties[name] = property
					continue
				}
				values, own := existing["enum"].([]any)
				more, other := property.(map[string]any)["enum"].([]any)
				if !own || !other {
					delete(existing, "enum")
					continue
				}
				for _, value := range more {
					if !slices.Contains(values, value) {
						values = append(values, value)
					}
				}
				existing["enum"] = values
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

// normalizeFunctionParameters 将空对象、空值与 true 归一化为对象 Schema
func normalizeFunctionParameters(raw json.RawMessage) (json.RawMessage, error) {
	cleaned, err := cleanSchemaInput(raw, true)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(cleaned, []byte("{}")) || bytes.Equal(cleaned, []byte("true")) {
		return json.RawMessage(`{"type":"object"}`), nil
	}
	return cleaned, nil
}

// decodeSchemaValue 解码 Schema 并保留数值原文
func decodeSchemaValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
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
	for _, name := range []string{"type", "format", "description", "nullable", "enum", "items", "additionalItems", "prefixItems", "properties", "required", "minItems", "maxItems", "minProperties", "maxProperties", "minimum", "maximum", "minLength", "maxLength", "pattern", "oneOf", "anyOf", "allOf", "not", "propertyOrdering"} {
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
	for _, name := range []string{"items", "additionalItems", "not"} {
		nested := bytes.TrimSpace(schema[name])
		if len(nested) > 0 && nested[0] == '{' {
			cleaned, err := cleanSchemaInput(nested, preserveCombinations)
			if err != nil {
				return nil, fmt.Errorf("schema.%s: %w", name, err)
			}
			schema[name] = cleaned
		} else if name == "items" && len(nested) > 0 && nested[0] == '[' {
			var tuple []json.RawMessage
			if json.Unmarshal(nested, &tuple) != nil {
				return nil, fmt.Errorf("schema.items 必须是 Schema 或 Schema 数组")
			}
			for index, item := range tuple {
				if bytes.Equal(bytes.TrimSpace(item), []byte("null")) {
					item = json.RawMessage("true")
				}
				cleaned, err := cleanSchemaInput(item, preserveCombinations)
				if err != nil {
					return nil, fmt.Errorf("schema.items[%d]: %w", index, err)
				}
				tuple[index] = cleaned
			}
			schema[name], _ = json.Marshal(tuple)
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

// typeKeywords 为只对数组或对象类型有意义的关键字，多类型 type 展开为 anyOf 时移入对应类型的分支
var typeKeywords = map[string][]string{
	"array":  {"items", "additionalItems", "prefixItems", "minItems", "maxItems"},
	"object": {"properties", "required", "additionalProperties", "minProperties", "maxProperties", "propertyNames"},
}

// normalizeNullableVariants 将 JSON Schema null 联合映射为 nullable，多类型 type 展开为各类型一个分支的 anyOf，数组与对象关键字随类型移入分支
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
			schema["type"], _ = json.Marshal(nonNull[0])
			if len(nonNull) > 1 {
				delete(schema, "type")
				variants := make([]map[string]json.RawMessage, 0, len(nonNull))
				for _, name := range nonNull {
					encodedName, _ := json.Marshal(name)
					variant := map[string]json.RawMessage{"type": encodedName}
					for _, keyword := range typeKeywords[strings.ToLower(name)] {
						if value, ok := schema[keyword]; ok {
							variant[keyword] = value
						}
					}
					variants = append(variants, variant)
				}
				for _, name := range nonNull {
					for _, keyword := range typeKeywords[strings.ToLower(name)] {
						delete(schema, keyword)
					}
				}
				schema["anyOf"], _ = json.Marshal(variants)
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
			if typeName, err := schemaString(value["type"], "type"); err == nil && strings.EqualFold(typeName, "null") {
				nullable = true
				continue
			}
			filtered = append(filtered, variant)
		}
		if !nullable {
			continue
		}
		if len(filtered) == 0 {
			return fmt.Errorf("schema.%s 必须包含非 null 类型", name)
		}
		schema[name], _ = json.Marshal(filtered)
		schema["nullable"] = json.RawMessage("true")
	}
	return nil
}

// normalizeConstAndMetadata 将字符串常量和说明字段转换为可发送结构，去掉 title、$id、$comment 与 deprecated
func normalizeConstAndMetadata(schema map[string]json.RawMessage) error {
	delete(schema, "title")
	delete(schema, "$id")
	delete(schema, "$comment")
	delete(schema, "deprecated")
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
		schema["enum"], _ = json.Marshal([]string{value})
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
			variants[index], _ = json.Marshal(subSchema)
		}
		schema[name], _ = json.Marshal(variants)
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
func normalizeImplicitType(schema map[string]json.RawMessage) {
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
		case schema["enum"] != nil:
			schema["type"] = enumType(schema["enum"])
		case schema["pattern"] != nil || schema["format"] != nil || schema["minLength"] != nil || schema["maxLength"] != nil:
			schema["type"] = json.RawMessage(`"string"`)
		}
		if typed != nil && !conflictingType {
			schema["type"] = typed["type"]
			if items, ok := typed["items"]; ok && schema["items"] == nil && !conflictingItems && !branchesHave(schema["allOf"], "items") && !branchesHave(schema["allOf"], "prefixItems") {
				if trimmed := bytes.TrimSpace(items); len(trimmed) > 0 && trimmed[0] != '[' && !bytes.Equal(trimmed, []byte("false")) {
					schema["items"] = items
				}
			}
		}
	}
	var typeName string
	if json.Unmarshal(schema["type"], &typeName) != nil || !strings.EqualFold(typeName, "array") || schema["items"] != nil || branchesHave(schema["allOf"], "items") || branchesHave(schema["allOf"], "prefixItems") {
		return
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
			schema["items"], _ = json.Marshal(map[string]any{"anyOf": typed})
		}
	}
}

// branchesHave 判断 Schema 数组中是否有分支带 name 字段
func branchesHave(raw json.RawMessage, name string) bool {
	var variants []map[string]json.RawMessage
	json.Unmarshal(raw, &variants)
	return slices.ContainsFunc(variants, func(variant map[string]json.RawMessage) bool { return variant[name] != nil })
}

// enumType 返回 enum 取值的 JSON Schema 类型：全为整数为 integer，全为数字为 number，全为布尔值为 boolean，其余为 string
func enumType(raw json.RawMessage) json.RawMessage {
	var values []any
	json.Unmarshal(raw, &values)
	kind := ""
	for _, value := range values {
		next := "string"
		switch value := value.(type) {
		case float64:
			next = "number"
			if math.Trunc(value) == value {
				next = "integer"
			}
		case bool:
			next = "boolean"
		}
		switch {
		case kind == "" || kind == next:
			kind = next
		case kind == "integer" && next == "number" || kind == "number" && next == "integer":
			kind = "number"
		default:
			kind = "string"
		}
	}
	return json.RawMessage(`"` + cmp.Or(kind, "string") + `"`)
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
		} else if _, typed := result["type"]; !typed {
			encoded, _ := json.Marshal(values)
			result["type"] = strings.Trim(string(enumType(encoded)), `"`)
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

// schemaNumber 读取数值约束，JSON 数字与内容为数字的字符串都接受
func schemaNumber(raw json.RawMessage, name string) (float64, error) {
	text := string(raw)
	var quoted string
	if json.Unmarshal(raw, &quoted) == nil {
		text = strings.TrimSpace(quoted)
	}
	value, err := strconv.ParseFloat(text, 64)
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
