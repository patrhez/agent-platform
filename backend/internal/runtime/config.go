package runtime

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Definition is a release-bundled Agent configuration.
type Definition struct {
	Agent AgentDefinition `yaml:"agent"`
}

// AgentDefinition contains the model, limits, and MCP allowlist for one Agent.
type AgentDefinition struct {
	ID                  string          `yaml:"id"`
	Version             string          `yaml:"version"`
	Runtime             string          `yaml:"runtime"`
	Model               ModelDefinition `yaml:"model"`
	Limits              LimitDefinition `yaml:"limits"`
	MCPServers          []MCPServer     `yaml:"mcp_servers"`
	SkillsBundleVersion string          `yaml:"skills_bundle_version"`
}

// ModelDefinition selects the LLM gateway interface and model settings.
type ModelDefinition struct {
	APIMode     string  `yaml:"api_mode"`
	Model       string  `yaml:"model"`
	Temperature float32 `yaml:"temperature"`
}

// LimitDefinition bounds one Run's time and ReAct iterations.
type LimitDefinition struct {
	MaxSteps          int `yaml:"max_steps"`
	RunTimeoutSeconds int `yaml:"run_timeout_seconds"`
}

// MCPServer defines one remote MCP server available to an Agent.
type MCPServer struct {
	Key          string   `yaml:"key"`
	URL          string   `yaml:"url"`
	AllowedTools []string `yaml:"allowed_tools"`
}

// LoadDefinition loads and validates a bundled Agent definition.
func LoadDefinition(path string, workspaceMCPURL string, modelName string) (Definition, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("read Agent definition: %w", err)
	}
	definition := Definition{}
	if err := yaml.Unmarshal(contents, &definition); err != nil {
		return Definition{}, fmt.Errorf("decode Agent definition: %w", err)
	}
	definition.expandWorkspaceURL(workspaceMCPURL)
	definition.expandModelName(modelName)
	if err := definition.Validate(); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

func (definition *Definition) expandModelName(modelName string) {
	definition.Agent.Model.Model = strings.ReplaceAll(definition.Agent.Model.Model, "${LLM_MODEL}", modelName)
}

// Validate ensures the definition is safe for the MVP runtime.
func (definition Definition) Validate() error {
	agent := definition.Agent
	if agent.ID == "" || agent.Version == "" || agent.Runtime != Name {
		return fmt.Errorf("invalid Agent identity or runtime")
	}
	if !supportedAPIMode(agent.Model.APIMode) || agent.Model.Model == "" {
		return fmt.Errorf("unsupported model API mode %q", agent.Model.APIMode)
	}
	if agent.Limits.MaxSteps < 1 || agent.Limits.RunTimeoutSeconds < 1 {
		return fmt.Errorf("invalid Agent execution limits")
	}
	workspace, found := definition.WorkspaceServer()
	if !found || workspace.URL == "" || !hasExactlyWorkspaceTools(workspace.AllowedTools) {
		return fmt.Errorf("Agent must configure only the workspace read Tools")
	}
	return nil
}

// WorkspaceServer returns the sole Workspace MCP server enabled for the MVP.
func (definition Definition) WorkspaceServer() (MCPServer, bool) {
	for _, server := range definition.Agent.MCPServers {
		if server.Key == "workspace" {
			return server, true
		}
	}
	return MCPServer{}, false
}

func (definition *Definition) expandWorkspaceURL(workspaceMCPURL string) {
	for index := range definition.Agent.MCPServers {
		server := &definition.Agent.MCPServers[index]
		server.URL = strings.ReplaceAll(server.URL, "${WORKSPACE_MCP_URL}", workspaceMCPURL)
	}
}

func hasExactlyWorkspaceTools(tools []string) bool {
	if len(tools) != 3 {
		return false
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		seen[tool] = true
	}
	return seen["workspace.list_repositories"] && seen["code.search"] && seen["file.read"]
}

func supportedAPIMode(mode string) bool {
	return mode == "chat_completions" || mode == "responses"
}
