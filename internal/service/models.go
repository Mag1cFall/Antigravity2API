package service

import (
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

var (
	// datedSuffix 匹配客户端模型名末尾的日期版本，例如 -20261001 与 @20261001
	datedSuffix = regexp.MustCompile(`[-@]\d{8}$`)
	// effortSuffix 匹配模型名末尾括号中的思考档位或思考预算，例如 (high) 与 (8192)
	effortSuffix = regexp.MustCompile(`\(([a-z]+|-?\d+)\)$`)
	// levelLabel 匹配目录显示名末尾的档位，例如 " (Medium)"
	levelLabel = regexp.MustCompile(` \((Low|Medium|High)\)$`)
)

// defaultLevel 为请求不带思考参数时族模型使用的档位
const defaultLevel = "medium"

// resolve 把客户端模型名解析为目录条目，括号后缀不是思考档位或预算时返回 400，只在未就绪账户上一次目录中时返回 503，找不到时返回 404：先按 MODEL_MAPPING 替换，目录中有该 ID 时直接使用；否则去掉 models/ 前缀、括号档位、日期版本、-latest、-thinking 与档位后缀得到族名，档位依次取名称中指定的档位（括号中的档位或思考预算、-thinking 为 high、档位后缀）、思考参数与 medium、high、low 中目录有的第一个，最后按族名本身匹配
func (s *Service) resolve(name string, config antigravity.GenerationConfig) (antigravity.CatalogModel, error) {
	for _, alias := range s.live.Load().ModelMapping {
		if alias.Alias == name {
			name = alias.Model
			break
		}
	}
	if model, ok := s.pool.Model(name); ok {
		return model, nil
	}
	family := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "models/"))
	implied := ""
	if match := effortSuffix.FindStringSubmatch(family); match != nil {
		family = strings.TrimSuffix(family, match[0])
		if budget, err := strconv.ParseInt(match[1], 10, 64); err == nil {
			implied = effortLevel(antigravity.GenerationConfig{ThinkingBudget: &budget})
		} else if implied = effortLevel(antigravity.GenerationConfig{ReasoningEffort: match[1]}); implied == "" && match[1] != "auto" {
			return antigravity.CatalogModel{}, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("模型名后缀 %s 不是思考档位（none、minimal、low、medium、high、xhigh、max、auto）或思考预算", match[0])}
		}
	}
	family = strings.TrimSuffix(datedSuffix.ReplaceAllString(family, ""), "-latest")
	if trimmed, ok := strings.CutSuffix(family, "-thinking"); ok {
		family, implied = trimmed, "high"
	}
	for _, suffix := range tierSuffixes {
		if trimmed, ok := strings.CutSuffix(family, suffix); ok {
			family, implied = trimmed, suffix[1:]
			break
		}
	}
	candidates := []string{name}
	for _, level := range []string{implied, effortLevel(config), defaultLevel, "high", "low"} {
		if level != "" {
			candidates = append(candidates, family+"-"+level)
		}
	}
	candidates = append(candidates, family)
	for _, id := range candidates[1:] {
		if model, ok := s.pool.Model(id); ok {
			return model, nil
		}
	}
	if reasons := s.pending(candidates); len(reasons) > 0 {
		return antigravity.CatalogModel{}, &Error{Status: http.StatusServiceUnavailable, Message: fmt.Sprintf("模型 %s 所在的账户尚未就绪\n%s", name, strings.Join(reasons, "\n"))}
	}
	return antigravity.CatalogModel{}, &Error{Status: http.StatusNotFound, Message: fmt.Sprintf("模型 %s 不在账户目录中", name)}
}

// pending 返回上一次目录中含 ids 之一的未就绪已启用账户及其原因，初始化未结束的账户原因为初始化中
func (s *Service) pending(ids []string) []string {
	records := s.store.Accounts()
	s.mu.Lock()
	defer s.mu.Unlock()
	var reasons []string
	for _, record := range records {
		if _, ready := s.pool.Status(record.ID); ready || !record.Enabled {
			continue
		}
		if !slices.ContainsFunc(s.catalogs[record.ID], func(model antigravity.CatalogModel) bool { return slices.Contains(ids, model.ID) }) {
			continue
		}
		reason := s.failures[record.ID]
		if reason == "" {
			reason = "初始化中"
		}
		reasons = append(reasons, record.ID+": "+reason)
	}
	return reasons
}

// public 把目录条目投影为公开目录：Agent 模型中 ID 档位后缀与显示名档位一致的条目按族合并为一个族 ID，列出可选档位，名称去掉档位，上限与能力取默认档位的条目；默认档位依次取 medium、high、low 中族内有的第一个，与 resolve 一致；其余条目原样公开
func public(catalog []antigravity.CatalogModel) []antigravity.Model {
	var models []antigravity.Model
	index := make(map[string]int)
	members := make(map[string]map[string]antigravity.CatalogModel)
	for _, entry := range catalog {
		family, level := familyOf(entry)
		if family == "" {
			models = append(models, entry.Public())
			continue
		}
		if _, ok := index[family]; !ok {
			index[family] = len(models)
			members[family] = make(map[string]antigravity.CatalogModel)
			models = append(models, antigravity.Model{})
		}
		members[family][level] = entry
	}
	for family, entries := range members {
		var levels []string
		for _, level := range []string{"low", "medium", "high"} {
			if _, ok := entries[level]; ok {
				levels = append(levels, level)
			}
		}
		for _, level := range []string{defaultLevel, "high", "low"} {
			if entry, ok := entries[level]; ok {
				model := entry.Public()
				model.ID, model.Name, model.ThinkingLevels, model.DefaultThinkingLevel = family, levelLabel.ReplaceAllString(entry.DisplayName, ""), levels, level
				models[index[family]] = model
				break
			}
		}
	}
	return models
}

// familyOf 返回 Agent 模型条目的族名与档位，ID 后缀与显示名档位不一致或不是 Agent 模型时族名为空
func familyOf(entry antigravity.CatalogModel) (family, level string) {
	match := levelLabel.FindStringSubmatch(entry.DisplayName)
	if !entry.Agent || match == nil {
		return "", ""
	}
	level = strings.ToLower(match[1])
	family, ok := strings.CutSuffix(entry.ID, "-"+level)
	if !ok {
		return "", ""
	}
	return family, level
}

// aliases 把 MODEL_MAPPING 中目标可用的别名加入公开目录，字段取自目标模型，与目录 ID 同名的别名替换该条目；同名别名以先写的为准，与 resolve 一致
func (s *Service) aliases(models []antigravity.Model) []antigravity.Model {
	index := make(map[string]int, len(models))
	for i, model := range models {
		index[model.ID] = i
	}
	done := make(map[string]bool)
	for _, alias := range s.live.Load().ModelMapping {
		if done[alias.Alias] {
			continue
		}
		done[alias.Alias] = true
		target, err := s.resolve(alias.Model, antigravity.GenerationConfig{})
		if err != nil {
			continue
		}
		model := target.Public()
		model.ID = alias.Alias
		if i, ok := index[alias.Alias]; ok {
			models[i] = model
			continue
		}
		index[alias.Alias] = len(models)
		models = append(models, model)
	}
	return models
}

// ModelAccounts 返回各模型可调度到的就绪账户数，键为公开模型 ID、目录条目 ID 与 MODEL_MAPPING 别名，别名（含与目录 ID 同名的别名）取其目标解析到的目录条目的值
func (s *Service) ModelAccounts() map[string]int {
	counts := make(map[string]int)
	s.mu.Lock()
	for id, models := range s.catalogs {
		if _, ready := s.pool.Status(id); !ready {
			continue
		}
		ids := make(map[string]bool)
		for _, model := range models {
			ids[model.ID] = true
		}
		for _, model := range public(models) {
			ids[model.ID] = true
		}
		for model := range ids {
			counts[model]++
		}
	}
	s.mu.Unlock()
	catalog := maps.Clone(counts)
	done := make(map[string]bool)
	for _, alias := range s.live.Load().ModelMapping {
		if done[alias.Alias] {
			continue
		}
		done[alias.Alias] = true
		if target, err := s.resolve(alias.Model, antigravity.GenerationConfig{}); err == nil {
			counts[alias.Alias] = catalog[target.ID]
		}
	}
	return counts
}

// lowerTier 判断模型是否在 PREFER_LOWER_TIER_MODELS 中，按目录 ID 或去掉档位后缀的族 ID 匹配
func (s *Service) lowerTier(model antigravity.CatalogModel) bool {
	models := s.live.Load().PreferLowerTierModels
	family := model.ID
	for _, suffix := range tierSuffixes {
		if trimmed, ok := strings.CutSuffix(family, suffix); ok {
			family = trimmed
			break
		}
	}
	return slices.Contains(models, model.ID) || slices.Contains(models, family)
}
