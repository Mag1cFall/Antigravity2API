package prompt

// officialTools 为官方客户端 agent 请求声明的工具名，顺序同请求的 tools
var officialTools = []tool{"read_file", "run_command", "list_plugin_accounts", "manage_task", "send_message", "schedule", "invoke_subagent", "define_subagent", "manage_subagents", "write_file", "edit_file", "search_web", "search_marketplace", "swarm", "ask_question"}

// templateData 为服务端段模板的数据：官方段模板的数据与官方客户端 agent 请求的执行配置
type templateData struct {
	systemData
	CascadeConfig cascadeConfig
}

// cascadeConfig 为服务端段模板读取的执行配置
type cascadeConfig struct{}

// GetPlannerConfig 返回规划器配置
func (cascadeConfig) GetPlannerConfig() plannerConfig {
	return plannerConfig{}
}

// plannerConfig 为执行配置中的规划器配置
type plannerConfig struct{}

// GetDeclarativeMixinConfig 返回声明式工具配置
func (plannerConfig) GetDeclarativeMixinConfig() *mixinConfig {
	return &mixinConfig{}
}

// GetToolConfig 返回工具开关配置
func (plannerConfig) GetToolConfig() toolConfig {
	return toolConfig{}
}

// mixinConfig 为声明式工具配置：官方工具，没有内置提示段
type mixinConfig struct{}

// GetPromptSections 返回内置提示段，官方客户端没有
func (*mixinConfig) GetPromptSections() []any {
	return nil
}

// GetTools 返回声明的工具
func (*mixinConfig) GetTools() []tool {
	return officialTools
}

// tool 为一个声明的工具名
type tool string

// GetName 返回工具名
func (t tool) GetName() string {
	return string(t)
}

// toolConfig 为工具开关配置，各工具都未强制关闭
type toolConfig struct{}

// GetViewFile 返回 view_file 的开关
func (toolConfig) GetViewFile() toolSwitch {
	return toolSwitch{}
}

// GetCodeSearch 返回 code_search 的开关
func (toolConfig) GetCodeSearch() toolSwitch {
	return toolSwitch{}
}

// GetRunCommand 返回 run_command 的配置
func (toolConfig) GetRunCommand() runCommandConfig {
	return runCommandConfig{}
}

// toolSwitch 为一个工具的开关
type toolSwitch struct{}

// GetForceDisable 判断工具是否强制关闭
func (toolSwitch) GetForceDisable() bool {
	return false
}

// runCommandConfig 为 run_command 的配置：允许搜索命令，搜索回退方式未指定
type runCommandConfig struct{}

// GetForbidSearchCommands 判断是否禁止搜索命令
func (runCommandConfig) GetForbidSearchCommands() bool {
	return false
}

// GetSearchFallbackMode 返回搜索回退方式
func (runCommandConfig) GetSearchFallbackMode() string {
	return "SEARCH_FALLBACK_MODE_UNSPECIFIED"
}
