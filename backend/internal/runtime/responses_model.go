package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const maximumResponseErrorBody = 8 * 1024

// ResponsesModel adapts an OpenAI-compatible Responses endpoint to Eino's model interface.
type ResponsesModel struct {
	endpoint    string
	apiKey      string
	model       string
	temperature float32
	client      *http.Client
	tools       []*schema.ToolInfo
}

type responsesRequest struct {
	Model             string            `json:"model"`
	Input             []json.RawMessage `json:"input"`
	Tools             []responseTool    `json:"tools,omitempty"`
	Temperature       float32           `json:"temperature"`
	ParallelToolCalls bool              `json:"parallel_tool_calls"`
	Store             bool              `json:"store"`
}

type responseInput struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   any    `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type responseTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type responsesResponse struct {
	Output []json.RawMessage `json:"output"`
}

type responseOutput struct {
	Type      string               `json:"type"`
	CallID    string               `json:"call_id"`
	Name      string               `json:"name"`
	Arguments string               `json:"arguments"`
	Content   []responseTextOutput `json:"content"`
}

type responseTextOutput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// newResponsesModel creates an Eino model backed by the Responses API.
func newResponsesModel(definition ModelDefinition, baseURL string, apiKey string) (*ResponsesModel, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("LLM_BASE_URL is required for the Responses model")
	}
	return &ResponsesModel{
		endpoint:    strings.TrimRight(baseURL, "/") + "/responses",
		apiKey:      apiKey,
		model:       definition.Model,
		temperature: definition.Temperature,
		client:      &http.Client{Timeout: 10 * time.Minute},
	}, nil
}

// WithTools returns a copy of the model bound to an immutable Tool set.
func (responsesModel *ResponsesModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	copy := *responsesModel
	copy.tools = append([]*schema.ToolInfo(nil), tools...)
	return &copy, nil
}

// Generate invokes the Responses API and converts its text or function calls to an Eino Message.
func (responsesModel *ResponsesModel) Generate(
	ctx context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	request, err := responsesModel.newRequest(messages, options...)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode Responses request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, responsesModel.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Responses request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if responsesModel.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+responsesModel.apiKey)
	}
	response, err := responsesModel.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("call Responses API: %w", err)
	}
	message, decodeErr := decodeResponsesResponse(response)
	closeErr := response.Body.Close()
	if decodeErr != nil {
		if closeErr != nil {
			return nil, errors.Join(decodeErr, fmt.Errorf("close Responses response: %w", closeErr))
		}
		return nil, decodeErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close Responses response: %w", closeErr)
	}
	return message, nil
}

func decodeResponsesResponse(response *http.Response) (*schema.Message, error) {
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, responseError(response)
	}
	decoded := responsesResponse{}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode Responses API response: %w", err)
	}
	return convertResponse(decoded)
}

// Stream returns a one-element stream because platform token streaming is handled separately.
func (responsesModel *ResponsesModel) Stream(
	ctx context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	message, err := responsesModel.Generate(ctx, messages, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (responsesModel *ResponsesModel) newRequest(
	messages []*schema.Message,
	options ...model.Option,
) (responsesRequest, error) {
	common := model.GetCommonOptions(nil, options...)
	tools := responsesModel.tools
	if common.Tools != nil {
		tools = common.Tools
	}
	convertedTools, err := convertTools(tools)
	if err != nil {
		return responsesRequest{}, err
	}
	input, err := convertMessages(messages)
	if err != nil {
		return responsesRequest{}, err
	}
	modelName := responsesModel.model
	if common.Model != nil {
		modelName = *common.Model
	}
	temperature := responsesModel.temperature
	if common.Temperature != nil {
		temperature = *common.Temperature
	}
	return responsesRequest{
		Model:             modelName,
		Input:             input,
		Tools:             convertedTools,
		Temperature:       temperature,
		ParallelToolCalls: false,
		Store:             false,
	}, nil
}

func convertMessages(messages []*schema.Message) ([]json.RawMessage, error) {
	converted := make([]json.RawMessage, 0, len(messages))
	var err error
	for _, message := range messages {
		switch message.Role {
		case schema.System, schema.User:
			item := responseInput{
				Role:    string(message.Role),
				Content: []map[string]string{{"type": "input_text", "text": message.Content}},
			}
			converted, err = appendResponseInput(converted, item)
			if err != nil {
				return nil, err
			}
		case schema.Assistant:
			nativeItems, err := responseOutputItems(message.Extra)
			if err != nil {
				return nil, err
			}
			if len(nativeItems) > 0 {
				converted = append(converted, nativeItems...)
				continue
			}
			for _, call := range message.ToolCalls {
				converted, err = appendResponseInput(converted, responseInput{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				})
				if err != nil {
					return nil, err
				}
			}
			if message.Content != "" {
				converted, err = appendResponseInput(
					converted, responseInput{Role: "assistant", Content: message.Content},
				)
				if err != nil {
					return nil, err
				}
			}
		case schema.Tool:
			converted, err = appendResponseInput(converted, responseInput{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: message.Content,
			})
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported Responses message role %q", message.Role)
		}
	}
	return converted, nil
}

func appendResponseInput(
	values []json.RawMessage,
	input responseInput,
) ([]json.RawMessage, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode Responses input item: %w", err)
	}
	return append(values, encoded), nil
}

func responseOutputItems(extra map[string]any) ([]json.RawMessage, error) {
	if len(extra) == 0 || extra["openai_responses_output"] == nil {
		return nil, nil
	}
	switch values := extra["openai_responses_output"].(type) {
	case []json.RawMessage:
		return values, nil
	case []any:
		items := make([]json.RawMessage, 0, len(values))
		for _, value := range values {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("encode restored Responses output item: %w", err)
			}
			items = append(items, encoded)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("invalid restored Responses output item representation")
	}
}

type responseReplayHeader struct {
	Type string `json:"type"`
	Role string `json:"role"`
}

func restoreResponsesReplayItems(items []json.RawMessage) ([]*schema.Message, error) {
	messages := make([]*schema.Message, 0, len(items))
	pending := schema.AssistantMessage("", nil)
	pendingItems := make([]json.RawMessage, 0)
	flushAssistant := func() {
		if pending.Content == "" && len(pending.ToolCalls) == 0 && len(pendingItems) == 0 {
			return
		}
		pending.Extra = map[string]any{
			"openai_responses_output": append([]json.RawMessage(nil), pendingItems...),
		}
		messages = append(messages, pending)
		pending = schema.AssistantMessage("", nil)
		pendingItems = pendingItems[:0]
	}
	for _, item := range items {
		header := responseReplayHeader{}
		if err := json.Unmarshal(item, &header); err != nil {
			return nil, fmt.Errorf("decode Responses replay item header: %w", err)
		}
		switch {
		case header.Role == "system" || header.Role == "user":
			flushAssistant()
			content, err := responseReplayContent(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, &schema.Message{Role: schema.RoleType(header.Role), Content: content})
		case header.Type == "function_call":
			output := responseOutput{}
			if err := json.Unmarshal(item, &output); err != nil {
				return nil, fmt.Errorf("decode Responses function-call replay item: %w", err)
			}
			pending.ToolCalls = append(pending.ToolCalls, schema.ToolCall{
				ID: output.CallID, Type: "function",
				Function: schema.FunctionCall{Name: output.Name, Arguments: output.Arguments},
			})
			pendingItems = append(pendingItems, append(json.RawMessage(nil), item...))
		case header.Type == "message":
			output := responseOutput{}
			if err := json.Unmarshal(item, &output); err != nil {
				return nil, fmt.Errorf("decode Responses message replay item: %w", err)
			}
			for _, content := range output.Content {
				if content.Type == "output_text" {
					pending.Content += content.Text
				}
			}
			pendingItems = append(pendingItems, append(json.RawMessage(nil), item...))
			flushAssistant()
		case header.Type == "function_call_output":
			flushAssistant()
			input := responseInput{}
			if err := json.Unmarshal(item, &input); err != nil {
				return nil, fmt.Errorf("decode Responses tool-result replay item: %w", err)
			}
			messages = append(messages, schema.ToolMessage(input.Output, input.CallID))
		default:
			pendingItems = append(pendingItems, append(json.RawMessage(nil), item...))
		}
	}
	flushAssistant()
	return messages, nil
}

func responseReplayContent(item json.RawMessage) (string, error) {
	input := responseInput{}
	if err := json.Unmarshal(item, &input); err != nil {
		return "", fmt.Errorf("decode Responses message replay item: %w", err)
	}
	switch content := input.Content.(type) {
	case string:
		return content, nil
	case []any:
		var text strings.Builder
		for _, part := range content {
			values, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if value, ok := values["text"].(string); ok {
				text.WriteString(value)
			}
		}
		return text.String(), nil
	default:
		return "", fmt.Errorf("unsupported Responses replay message content")
	}
}

func convertTools(tools []*schema.ToolInfo) ([]responseTool, error) {
	converted := make([]responseTool, 0, len(tools))
	for _, tool := range tools {
		parameters, err := tool.ParamsOneOf.ToJSONSchema()
		if err != nil {
			return nil, fmt.Errorf("convert Tool schema %s: %w", tool.Name, err)
		}
		encoded, err := json.Marshal(parameters)
		if err != nil {
			return nil, fmt.Errorf("encode Tool schema %s: %w", tool.Name, err)
		}
		converted = append(converted, responseTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Desc,
			Parameters:  encoded,
		})
	}
	return converted, nil
}

func convertResponse(response responsesResponse) (*schema.Message, error) {
	message := schema.AssistantMessage("", nil)
	for _, rawItem := range response.Output {
		item := responseOutput{}
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return nil, fmt.Errorf("decode Responses output item: %w", err)
		}
		if item.Type == "function_call" {
			message.ToolCalls = append(message.ToolCalls, schema.ToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
			continue
		}
		if item.Type == "message" {
			for _, content := range item.Content {
				if content.Type == "output_text" {
					message.Content += content.Text
				}
			}
		}
	}
	message.Extra = map[string]any{
		"openai_responses_output": append([]json.RawMessage(nil), response.Output...),
	}
	if message.Content == "" && len(message.ToolCalls) == 0 {
		return nil, fmt.Errorf("Responses API returned no text or function call")
	}
	return message, nil
}

func responseError(response *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseErrorBody))
	if err != nil {
		return fmt.Errorf("read Responses API error: %w", err)
	}
	return fmt.Errorf("Responses API returned %s: %s", response.Status, strings.TrimSpace(string(body)))
}
