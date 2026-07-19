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
	Model             string          `json:"model"`
	Input             []responseInput `json:"input"`
	Tools             []responseTool  `json:"tools,omitempty"`
	Temperature       float32         `json:"temperature"`
	ParallelToolCalls bool            `json:"parallel_tool_calls"`
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
	Output []responseOutput `json:"output"`
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
	}, nil
}

func convertMessages(messages []*schema.Message) ([]responseInput, error) {
	converted := make([]responseInput, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case schema.System, schema.User:
			converted = append(converted, responseInput{
				Role:    string(message.Role),
				Content: []map[string]string{{"type": "input_text", "text": message.Content}},
			})
		case schema.Assistant:
			for _, call := range message.ToolCalls {
				converted = append(converted, responseInput{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				})
			}
			if message.Content != "" {
				converted = append(converted, responseInput{Role: "assistant", Content: message.Content})
			}
		case schema.Tool:
			converted = append(converted, responseInput{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: message.Content,
			})
		default:
			return nil, fmt.Errorf("unsupported Responses message role %q", message.Role)
		}
	}
	return converted, nil
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
	for _, item := range response.Output {
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
