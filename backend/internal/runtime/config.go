package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Definition is a release-bundled Agent configuration.
type Definition struct {
	Agent AgentDefinition `yaml:"agent"`
	// SystemPrompt is assembled from the versioned Skills bundle at load time.
	SystemPrompt string `yaml:"-"`
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
	// SafeArguments lists, per Tool, the argument keys that may appear in
	// user-visible events and logs. Unlisted arguments are redacted.
	SafeArguments map[string][]string `yaml:"safe_arguments"`
}

// LoadDefinition loads and validates a bundled Agent definition and its Skills bundle.
func LoadDefinition(path string, workspaceMCPURL string, modelName string) (Definition, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("read Agent definition: %w", err)
	}
	definition := Definition{}
	if err := yaml.Unmarshal(contents, &definition); err != nil {
		return Definition{}, fmt.Errorf("decode Agent definition: %w", err)
	}
	definition.expandServerURLs(workspaceMCPURL)
	definition.expandModelName(modelName)
	if err := definition.Validate(); err != nil {
		return Definition{}, err
	}
	prompt, err := LoadSkillsBundle(skillsRoot(path), definition.Agent.SkillsBundleVersion)
	if err != nil {
		return Definition{}, err
	}
	definition.SystemPrompt = prompt
	return definition, nil
}

// skillsRoot resolves the Skills bundle directory shipped next to the Agent definition.
// SKILLS_DIR overrides the default sibling "skills" directory.
func skillsRoot(definitionPath string) string {
	if fromEnv := os.Getenv("SKILLS_DIR"); fromEnv != "" {
		return fromEnv
	}
	return filepath.Join(filepath.Dir(definitionPath), "skills")
}

// LoadSkillsBundle concatenates the versioned Skills files into one system prompt.
// Files are joined in lexical filename order so bundles stay deterministic.
func LoadSkillsBundle(root string, version string) (string, error) {
	if version == "" {
		return "", fmt.Errorf("skills_bundle_version is required")
	}
	bundleDir := filepath.Join(root, version)
	entries, err := os.ReadDir(bundleDir)
	if err != nil {
		return "", fmt.Errorf("read Skills bundle %s: %w", bundleDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		return "", fmt.Errorf("Skills bundle %s contains no files", bundleDir)
	}
	sort.Strings(names)
	sections := make([]string, 0, len(names))
	for _, name := range names {
		contents, err := os.ReadFile(filepath.Join(bundleDir, name))
		if err != nil {
			return "", fmt.Errorf("read Skill file %s: %w", name, err)
		}
		section := strings.TrimSpace(string(contents))
		if section != "" {
			sections = append(sections, section)
		}
	}
	if len(sections) == 0 {
		return "", fmt.Errorf("Skills bundle %s contains only empty files", bundleDir)
	}
	return strings.Join(sections, "\n\n"), nil
}

func (definition *Definition) expandModelName(modelName string) {
	definition.Agent.Model.Model = strings.ReplaceAll(definition.Agent.Model.Model, "${LLM_MODEL}", modelName)
}

// Validate ensures the definition is complete and unambiguous.
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
	if agent.SkillsBundleVersion == "" {
		return fmt.Errorf("skills_bundle_version is required")
	}
	return validateMCPServers(agent.MCPServers)
}

func validateMCPServers(servers []MCPServer) error {
	if len(servers) == 0 {
		return fmt.Errorf("Agent must configure at least one MCP server")
	}
	seenKeys := map[string]bool{}
	seenModelNames := map[string]string{}
	for _, server := range servers {
		if server.Key == "" {
			return fmt.Errorf("MCP server key is required")
		}
		if seenKeys[server.Key] {
			return fmt.Errorf("duplicate MCP server key %q", server.Key)
		}
		seenKeys[server.Key] = true
		if server.URL == "" {
			return fmt.Errorf("MCP server %q requires a URL", server.Key)
		}
		if len(server.AllowedTools) == 0 {
			return fmt.Errorf("MCP server %q requires at least one allowed Tool", server.Key)
		}
		allowed := map[string]bool{}
		for _, tool := range server.AllowedTools {
			if tool == "" {
				return fmt.Errorf("MCP server %q has an empty Tool name", server.Key)
			}
			allowed[tool] = true
			modelName := ModelToolName(tool)
			if owner, taken := seenModelNames[modelName]; taken {
				return fmt.Errorf(
					"Tool %q on MCP server %q conflicts with a Tool on server %q (model name %q)",
					tool, server.Key, owner, modelName,
				)
			}
			seenModelNames[modelName] = server.Key
		}
		for tool := range server.SafeArguments {
			if !allowed[tool] {
				return fmt.Errorf("safe_arguments references Tool %q missing from allowed_tools of server %q", tool, server.Key)
			}
		}
	}
	return nil
}

// ModelToolName converts an MCP Tool name into a provider-compatible function name.
func ModelToolName(mcpToolName string) string {
	var builder strings.Builder
	for _, character := range mcpToolName {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '_', character == '-':
			builder.WriteRune(character)
		default:
			builder.WriteRune('_')
		}
	}
	return builder.String()
}

// expandServerURLs substitutes ${WORKSPACE_MCP_URL} and any other environment
// variables referenced by configured MCP server URLs.
func (definition *Definition) expandServerURLs(workspaceMCPURL string) {
	for index := range definition.Agent.MCPServers {
		server := &definition.Agent.MCPServers[index]
		server.URL = strings.ReplaceAll(server.URL, "${WORKSPACE_MCP_URL}", workspaceMCPURL)
		server.URL = os.ExpandEnv(server.URL)
	}
}

func supportedAPIMode(mode string) bool {
	return mode == "chat_completions" || mode == "responses"
}
