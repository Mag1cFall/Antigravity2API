package antigravity

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// flagMarketplaceSearch 为打开插件市场搜索工具与推荐插件段的实验开关
	flagMarketplaceSearch = "enable-marketplace-search-tool"
	// flagPluginMarketplaces 为插件市场目录的实验开关，每项为“目录名=地址”
	flagPluginMarketplaces = "agy-plugin-marketplaces"
	// flagPluginShelves 为推荐插件货架的实验开关，取值为货架 JSON
	flagPluginShelves = "agy-plugin-shelves"
	// featuredPerShelf 为每个货架列出的插件数上限
	featuredPerShelf = 10
	// featuredDescription 为推荐插件说明保留的字符数上限
	featuredDescription = 150
)

// MarketplaceCache 为虚拟 Hub 保存的一个插件市场目录：响应的 Last-Modified 与正文
type MarketplaceCache struct {
	LastModified string          `json:"last_modified,omitempty"`
	Body         json.RawMessage `json:"body"`
}

// marketplacePlugin 表示插件市场目录中的一个插件
type marketplacePlugin struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// marketplaceCatalog 表示一个插件市场目录
type marketplaceCatalog struct {
	Plugins []marketplacePlugin `json:"plugins"`
}

// marketplaceShelf 表示推荐插件货架开关中的一个货架
type marketplaceShelf struct {
	DisplayName      string   `json:"displayName"`
	Description      string   `json:"description"`
	Position         int      `json:"position"`
	CustomizationIDs []string `json:"customizationIds"`
}

// marketplaceEvent 表示一次插件市场目录读取的 clearcut 事件，at 为读取结束时刻
type marketplaceEvent struct {
	at     time.Time
	params map[string]any
}

// findFlag 返回同名实验开关
func findFlag(flags []experimentFlag, name string) (experimentFlag, bool) {
	index := slices.IndexFunc(flags, func(flag experimentFlag) bool { return flag.Name == name })
	if index < 0 {
		return experimentFlag{}, false
	}
	return flags[index], true
}

// marketplaceEnabled 判断实验开关是否打开插件市场搜索工具
func marketplaceEnabled(flags []experimentFlag) bool {
	flag, ok := findFlag(flags, flagMarketplaceSearch)
	return ok && flag.BoolValue != nil && *flag.BoolValue
}

// LoadMarketplace 按官方 language_server 在虚拟进程第一次生成前读取各插件市场目录：实验开关打开时每个虚拟进程只读一次，带保存的 Last-Modified 发出条件请求，读到目录后等 28–48 ms 再返回读取的开始时刻；没有读到目录时返回零值
func (c *Client) LoadMarketplace(ctx context.Context) time.Time {
	c.market.Lock()
	defer c.market.Unlock()
	c.mu.Lock()
	flags, fetched := c.state.ccpa.Flags, c.hub.fetched
	c.hub.fetched = true
	c.mu.Unlock()
	if fetched || !marketplaceEnabled(flags) {
		return time.Time{}
	}
	flag, _ := findFlag(flags, flagPluginMarketplaces)
	if flag.StringListValue == nil {
		return time.Time{}
	}
	started, loaded := time.Now(), false
	for _, value := range flag.StringListValue.Values {
		if name, address, ok := strings.Cut(value, "="); ok {
			loaded = c.fetchMarketplace(context.WithoutCancel(ctx), name, address) || loaded
		}
	}
	if !loaded {
		return time.Time{}
	}
	select {
	case <-time.After(time.Duration(between(28, 48)) * time.Millisecond):
	case <-ctx.Done():
	}
	return started
}

// fetchMarketplace 以 Hub User-Agent、no-cache 与保存的 Last-Modified 经 HTTP/2 读取一个目录，200 时以新的目录表替换并保存，200 或 304 时记下读取事件并返回 true
func (c *Client) fetchMarketplace(ctx context.Context, name, address string) bool {
	started := time.Now()
	c.mu.Lock()
	cached, save := c.hub.state.Marketplaces[name], c.hub.save
	c.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false
	}
	request.Header.Set("User-Agent", c.device.UserAgent())
	request.Header.Set("Cache-Control", "no-cache")
	if cached.LastModified != "" {
		request.Header.Set("If-Modified-Since", cached.LastModified)
	}
	status := 0
	defer func() { c.logRequest(request, status, started, err) }()
	response, err := c.web.Do(c.traced(request))
	if err != nil {
		return false
	}
	defer response.Body.Close()
	status = response.StatusCode
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return false
	}
	outcome := "not_modified_304"
	switch status {
	case http.StatusOK:
		outcome, cached = "success", MarketplaceCache{LastModified: response.Header.Get("Last-Modified"), Body: body}
	case http.StatusNotModified:
	default:
		return false
	}
	var catalog marketplaceCatalog
	if err = json.Unmarshal(cached.Body, &catalog); err != nil {
		return false
	}
	written := true
	if status == http.StatusOK {
		c.mu.Lock()
		marketplaces := make(map[string]MarketplaceCache, len(c.hub.state.Marketplaces)+1)
		maps.Copy(marketplaces, c.hub.state.Marketplaces)
		marketplaces[name] = cached
		c.hub.state.Marketplaces = marketplaces
		state := c.hub.state
		c.mu.Unlock()
		if save != nil {
			if err := save(state); err != nil {
				written = false
				slog.Warn("保存虚拟 Hub 状态失败 | 错误="+err.Error(), "source", c.account)
			}
		}
	}
	ended := time.Now()
	c.mu.Lock()
	c.hub.market = append(c.hub.market, marketplaceEvent{at: ended, params: map[string]any{
		"status": outcome, "failure_reason": "", "marketplace": name, "entry_count": len(catalog.Plugins), "skipped_entry_count": 0,
		"disk_cache_write_ok": strconv.FormatBool(written), "duration_ms": int(ended.Sub(started).Milliseconds()), "http_status": status,
	}})
	c.mu.Unlock()
	return true
}

// logMarketplace 在读取结束时刻记录待记录的插件市场目录读取事件
func (c *Client) logMarketplace() {
	c.mu.Lock()
	events := c.hub.market
	c.hub.market = nil
	c.mu.Unlock()
	for _, event := range events {
		c.hubEventAt(event.at, "marketplace_catalog_fetch", eventShape{flags: true}, event.params)
	}
}

// Plugins 返回推荐插件段的货架正文：实验开关打开时按货架开关与保存的插件市场目录生成，开关关闭或没有可列出的插件时为空
func (c *Client) Plugins() string {
	c.mu.Lock()
	flags, saved := c.state.ccpa.Flags, c.hub.state.Marketplaces
	c.mu.Unlock()
	if !marketplaceEnabled(flags) {
		return ""
	}
	catalogs := make(map[string]marketplaceCatalog, len(saved))
	for name, cached := range saved {
		var catalog marketplaceCatalog
		if json.Unmarshal(cached.Body, &catalog) == nil {
			catalogs[name] = catalog
		}
	}
	flag, _ := findFlag(flags, flagPluginShelves)
	return featuredPlugins(flag.value(), catalogs)
}

// featuredPlugins 生成推荐插件段的货架正文：货架按 position 排序，每个货架一行“名称: 说明”，其后列出目录中找得到的前 featuredPerShelf 个插件“- 名称 (ID): 说明”，货架后空一行；没有可列出插件的货架略去
func featuredPlugins(shelves string, catalogs map[string]marketplaceCatalog) string {
	var parsed struct {
		Shelves []marketplaceShelf `json:"shelves"`
	}
	if json.Unmarshal([]byte(shelves), &parsed) != nil {
		return ""
	}
	slices.SortStableFunc(parsed.Shelves, func(a, b marketplaceShelf) int { return a.Position - b.Position })
	var out strings.Builder
	for _, shelf := range parsed.Shelves {
		var lines strings.Builder
		listed := 0
		for _, id := range shelf.CustomizationIDs {
			parts := strings.Split(id, "/")
			if listed == featuredPerShelf || len(parts) != 3 || parts[1] != "plugin" {
				continue
			}
			plugins := catalogs[parts[0]].Plugins
			if index := slices.IndexFunc(plugins, func(plugin marketplacePlugin) bool { return plugin.Name == parts[2] }); index >= 0 {
				lines.WriteString("- " + plugins[index].DisplayName + " (" + id + "): " + featuredText(plugins[index].Description) + "\n")
				listed++
			}
		}
		if listed > 0 {
			out.WriteString(shelf.DisplayName + ": " + shelf.Description + "\n" + lines.String() + "\n")
		}
	}
	return out.String()
}

// featuredText 返回插件说明的第一句，超过 featuredDescription 个字符时截断并加省略号
func featuredText(text string) string {
	if index := strings.Index(text, ". "); index >= 0 {
		text = text[:index+1]
	}
	if runes := []rune(text); len(runes) > featuredDescription {
		text = string(runes[:featuredDescription]) + "…"
	}
	return text
}
