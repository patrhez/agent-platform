package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const validDefinition = `agent:
  id: test-agent
  version: 2026-07-19.1
  runtime: eino-react
  model:
    api_mode: chat_completions
    model: ${LLM_MODEL}
    temperature: 0.1
  limits:
    max_steps: 10
    run_timeout_seconds: 60
  mcp_servers:
    - key: workspace
      url: ${WORKSPACE_MCP_URL}
      allowed_tools: [code.search]
      safe_arguments:
        code.search: [repo, query]
    - key: extra
      url: http://extra:9000/mcp
      allowed_tools: [docs.lookup]
  skills_bundle_version: v1
`

func TestLoadDefinitionAssemblesSkillsPromptAndExpandsURLs(t *testing.T) {
	directory := t.TempDir()
	definitionPath := filepath.Join(directory, "agent.yaml")
	writeFile(t, definitionPath, validDefinition)
	writeFile(t, filepath.Join(directory, "skills", "v1", "10-base.md"), "Base skill.\n")
	writeFile(t, filepath.Join(directory, "skills", "v1", "20-extra.md"), "Extra skill.\n")

	definition, err := LoadDefinition(definitionPath, "http://workspace:8081/mcp", "model-x")
	if err != nil {
		t.Fatalf("LoadDefinition() error = %v", err)
	}
	if definition.SystemPrompt != "Base skill.\n\nExtra skill." {
		t.Errorf("SystemPrompt = %q", definition.SystemPrompt)
	}
	if definition.Agent.MCPServers[0].URL != "http://workspace:8081/mcp" {
		t.Errorf("workspace URL = %q", definition.Agent.MCPServers[0].URL)
	}
	if definition.Agent.MCPServers[1].URL != "http://extra:9000/mcp" {
		t.Errorf("extra URL = %q", definition.Agent.MCPServers[1].URL)
	}
	if definition.Agent.Model.Model != "model-x" {
		t.Errorf("model = %q", definition.Agent.Model.Model)
	}
}

func TestLoadDefinitionFailsWithoutSkillsBundle(t *testing.T) {
	directory := t.TempDir()
	definitionPath := filepath.Join(directory, "agent.yaml")
	writeFile(t, definitionPath, validDefinition)

	_, err := LoadDefinition(definitionPath, "http://workspace:8081/mcp", "model-x")
	if err == nil || !strings.Contains(err.Error(), "Skills bundle") {
		t.Fatalf("LoadDefinition() error = %v, want missing Skills bundle error", err)
	}
}

func TestValidateRejectsConflictingModelToolNames(t *testing.T) {
	definition := Definition{Agent: AgentDefinition{
		ID:      "test",
		Version: "v1",
		Runtime: Name,
		Model:   ModelDefinition{APIMode: "chat_completions", Model: "m"},
		Limits:  LimitDefinition{MaxSteps: 5, RunTimeoutSeconds: 60},
		MCPServers: []MCPServer{
			{Key: "one", URL: "http://one", AllowedTools: []string{"code.search"}},
			{Key: "two", URL: "http://two", AllowedTools: []string{"code_search"}},
		},
		SkillsBundleVersion: "v1",
	}}
	if err := definition.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want model Tool name conflict")
	}
}

func TestValidateRejectsSafeArgumentsForUnknownTool(t *testing.T) {
	definition := Definition{Agent: AgentDefinition{
		ID:      "test",
		Version: "v1",
		Runtime: Name,
		Model:   ModelDefinition{APIMode: "chat_completions", Model: "m"},
		Limits:  LimitDefinition{MaxSteps: 5, RunTimeoutSeconds: 60},
		MCPServers: []MCPServer{{
			Key:           "one",
			URL:           "http://one",
			AllowedTools:  []string{"code.search"},
			SafeArguments: map[string][]string{"file.read": {"repo"}},
		}},
		SkillsBundleVersion: "v1",
	}}
	if err := definition.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want unknown safe_arguments Tool")
	}
}

func TestValidateRejectsReductionWithoutSkipOffload(t *testing.T) {
	definition := Definition{Agent: AgentDefinition{
		ID:      "test",
		Version: "v1",
		Runtime: Name,
		Model:   ModelDefinition{APIMode: "chat_completions", Model: "m"},
		Limits:  LimitDefinition{MaxSteps: 5, RunTimeoutSeconds: 60},
		Context: ContextDefinition{
			Reduction: ReductionDefinition{Enabled: true, SkipOffload: false},
		},
		MCPServers: []MCPServer{{
			Key:          "one",
			URL:          "http://one",
			AllowedTools: []string{"code.search"},
		}},
		SkillsBundleVersion: "v1",
	}}
	if err := definition.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want skip_offload requirement")
	}
}

func TestValidateRejectsNegativeSummarizationThreshold(t *testing.T) {
	definition := Definition{Agent: AgentDefinition{
		ID:      "test",
		Version: "v1",
		Runtime: Name,
		Model:   ModelDefinition{APIMode: "chat_completions", Model: "m"},
		Limits:  LimitDefinition{MaxSteps: 5, RunTimeoutSeconds: 60},
		Context: ContextDefinition{
			Summarization: SummarizationDefinition{Enabled: true, ContextTokens: -1},
		},
		MCPServers: []MCPServer{{
			Key:          "one",
			URL:          "http://one",
			AllowedTools: []string{"code.search"},
		}},
		SkillsBundleVersion: "v1",
	}}
	if err := definition.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want negative summarization threshold error")
	}
}

func TestLoadDefinitionParsesContextMiddlewareConfig(t *testing.T) {
	directory := t.TempDir()
	definitionPath := filepath.Join(directory, "agent.yaml")
	writeFile(t, definitionPath, `agent:
  id: test-agent
  version: 2026-07-19.1
  runtime: eino-react
  model:
    api_mode: chat_completions
    model: ${LLM_MODEL}
    temperature: 0.1
  limits:
    max_steps: 10
    run_timeout_seconds: 60
  context:
    summarization:
      enabled: true
      context_tokens: 1000
      context_messages: 8
    reduction:
      enabled: true
      max_tokens_for_clear: 500
      max_length_for_trunc: 200
      skip_offload: true
  mcp_servers:
    - key: workspace
      url: ${WORKSPACE_MCP_URL}
      allowed_tools: [code.search]
      safe_arguments:
        code.search: [repo, query]
  skills_bundle_version: v1
`)
	writeFile(t, filepath.Join(directory, "skills", "v1", "10-base.md"), "Base skill.\n")

	definition, err := LoadDefinition(definitionPath, "http://workspace:8081/mcp", "model-x")
	if err != nil {
		t.Fatalf("LoadDefinition() error = %v", err)
	}
	if !definition.Agent.Context.Summarization.Enabled || definition.Agent.Context.Summarization.ContextTokens != 1000 {
		t.Fatalf("summarization = %#v", definition.Agent.Context.Summarization)
	}
	if !definition.Agent.Context.Reduction.Enabled || !definition.Agent.Context.Reduction.SkipOffload {
		t.Fatalf("reduction = %#v", definition.Agent.Context.Reduction)
	}
}
