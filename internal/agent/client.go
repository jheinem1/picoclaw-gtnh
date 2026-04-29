package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"greggpt-gtnh/internal/greggptauth"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const (
	DefaultCodexBaseURL = "https://chatgpt.com/backend-api/codex"

	gtnhWikiSearchDomain = "wiki.gtnewhorizons.com"

	codexOriginatorHeader  = "originator"
	codexOriginatorValue   = "codex_cli_rs"
	openAIBetaHeader       = "OpenAI-Beta"
	openAIBetaValue        = "responses=experimental"
	chatGPTAccountIDHeader = "Chatgpt-Account-Id"
)

type ResponseRequest = responses.ResponseNewParams
type Response = responses.Response

type ResponseCreator interface {
	CreateResponse(ctx context.Context, request ResponseRequest) (*Response, error)
}

type CodexClient struct {
	store       greggptauth.Store
	baseURL     string
	httpClient  *http.Client
	refreshOpts greggptauth.RefreshOptions
}

type CodexClientOptions struct {
	AuthFile       string
	BaseURL        string
	HTTPClient     *http.Client
	RefreshOptions greggptauth.RefreshOptions
}

func NewCodexClient(opts CodexClientOptions) *CodexClient {
	authFile := opts.AuthFile
	if authFile == "" {
		authFile = greggptauth.AuthFileFromEnv()
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = DefaultCodexBaseURL
	}
	return &CodexClient{
		store:       greggptauth.NewStore(authFile),
		baseURL:     baseURL,
		httpClient:  opts.HTTPClient,
		refreshOpts: opts.RefreshOptions,
	}
}

func (c *CodexClient) CreateResponse(ctx context.Context, request ResponseRequest) (*Response, error) {
	client, err := c.openAIClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.Responses.New(ctx, request)
}

func (c *CodexClient) CreateStreamingResponse(ctx context.Context, request ResponseRequest) (*Response, error) {
	client, err := c.openAIClient(ctx)
	if err != nil {
		return nil, err
	}

	stream := client.Responses.NewStreaming(ctx, request)
	defer stream.Close()

	var last *Response
	var output []responses.ResponseOutputItemUnion
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.created", "response.in_progress":
			resp := event.Response
			last = &resp
		case "response.output_item.done":
			output = append(output, event.AsResponseOutputItemDone().Item)
		case "response.completed":
			resp := event.AsResponseCompleted().Response
			if len(resp.Output) == 0 {
				resp.Output = output
			}
			return &resp, nil
		case "response.failed":
			resp := event.AsResponseFailed().Response
			return nil, fmt.Errorf("codex response failed: %s", responseErrorMessage(resp))
		case "response.incomplete":
			resp := event.AsResponseIncomplete().Response
			return nil, fmt.Errorf("codex response incomplete: %s", responseErrorMessage(resp))
		case "error":
			errEvent := event.AsError()
			return nil, fmt.Errorf("codex stream error: %s", errEvent.Message)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	if last != nil {
		if len(last.Output) == 0 {
			last.Output = output
		}
		return last, nil
	}
	return nil, fmt.Errorf("codex stream ended without a response")
}

func (c *CodexClient) openAIClient(ctx context.Context) (openai.Client, error) {
	creds, err := c.store.EnsureFresh(ctx, c.refreshOptions())
	if err != nil {
		return openai.Client{}, err
	}

	clientOpts := []option.RequestOption{
		option.WithBaseURL(c.baseURL),
		option.WithAPIKey(creds.Tokens.AccessToken),
		option.WithHeader(codexOriginatorHeader, codexOriginatorValue),
		option.WithHeader(openAIBetaHeader, openAIBetaValue),
	}
	if c.httpClient != nil {
		clientOpts = append(clientOpts, option.WithHTTPClient(c.httpClient))
	}
	if accountID := creds.AccountID(); accountID != "" {
		clientOpts = append(clientOpts, option.WithHeader(chatGPTAccountIDHeader, accountID))
	}

	return openai.NewClient(clientOpts...), nil
}

func (c *CodexClient) refreshOptions() greggptauth.RefreshOptions {
	opts := c.refreshOpts
	if opts.HTTPClient == nil {
		opts.HTTPClient = c.httpClient
	}
	return opts
}

type CodexAgentClient struct {
	raw       ResponseCreator
	streaming *CodexClient
}

func NewCodexAgentClient(opts CodexClientOptions) *CodexAgentClient {
	return &CodexAgentClient{streaming: NewCodexClient(opts)}
}

func NewCodexAgentClientFromCreator(raw ResponseCreator) *CodexAgentClient {
	return &CodexAgentClient{raw: raw}
}

func (c *CodexAgentClient) CreateResponse(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	if c.raw == nil && c.streaming == nil {
		return ModelResponse{}, fmt.Errorf("codex response creator is nil")
	}
	sdkRequest, err := toResponseRequest(request)
	if err != nil {
		return ModelResponse{}, err
	}
	var response *Response
	if c.streaming != nil {
		response, err = c.streaming.CreateStreamingResponse(ctx, sdkRequest)
	} else {
		response, err = c.raw.CreateResponse(ctx, sdkRequest)
	}
	if err != nil {
		return ModelResponse{}, err
	}
	if response == nil {
		return ModelResponse{}, fmt.Errorf("codex response is nil")
	}
	return fromResponse(response), nil
}

func toResponseRequest(request ModelRequest) (ResponseRequest, error) {
	input := make(responses.ResponseInputParam, 0, len(request.Input))
	for _, item := range request.Input {
		switch item.Role {
		case roleUser:
			input = append(input, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleUser,
					Content: responses.EasyInputMessageContentUnionParam{
						OfString: openai.String(item.Content),
					},
				},
			})
		case roleToolCall:
			if strings.TrimSpace(item.ToolCallID) == "" {
				return ResponseRequest{}, fmt.Errorf("tool call is missing call id")
			}
			if strings.TrimSpace(item.ToolName) == "" {
				return ResponseRequest{}, fmt.Errorf("tool call is missing name")
			}
			input = append(input, responses.ResponseInputItemParamOfFunctionCall(defaultJSON(item.Content), item.ToolCallID, item.ToolName))
		case roleToolOutput:
			if strings.TrimSpace(item.ToolCallID) == "" {
				return ResponseRequest{}, fmt.Errorf("tool output is missing call id")
			}
			input = append(input, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: item.ToolCallID,
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: openai.String(item.Content),
					},
				},
			})
		default:
			return ResponseRequest{}, fmt.Errorf("unsupported input role %q", item.Role)
		}
	}

	tools := make([]responses.ToolUnionParam, 0, len(request.Tools)+1)
	tools = append(tools, responses.ToolUnionParam{
		OfWebSearch: &responses.WebSearchToolParam{
			Type: responses.WebSearchToolTypeWebSearch,
			Filters: responses.WebSearchToolFiltersParam{
				AllowedDomains: []string{gtnhWikiSearchDomain},
			},
			SearchContextSize: responses.WebSearchToolSearchContextSizeMedium,
		},
	})
	for _, tool := range request.Tools {
		parameters := map[string]any{}
		if len(tool.Parameters) != 0 {
			if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
				return ResponseRequest{}, fmt.Errorf("parse parameters for tool %q: %w", tool.Name, err)
			}
		}
		tools = append(tools, responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:        tool.Name,
				Description: openai.String(tool.Description),
				Parameters:  parameters,
				Strict:      openai.Bool(false),
			},
		})
	}

	out := ResponseRequest{
		Model:        request.Model,
		Instructions: openai.String(request.Instructions),
		Store:        openai.Bool(false),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: input,
		},
		Tools:             tools,
		Include:           []responses.ResponseIncludable{responses.ResponseIncludableWebSearchCallActionSources},
		ParallelToolCalls: openai.Bool(true),
	}
	if effort := reasoningEffort(request.ReasoningEffort); effort != "" {
		out.Reasoning = shared.ReasoningParam{Effort: effort}
	}
	if request.PreviousResponseID != "" {
		out.PreviousResponseID = openai.String(request.PreviousResponseID)
	}
	return out, nil
}

func reasoningEffort(raw string) shared.ReasoningEffort {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none":
		return shared.ReasoningEffortNone
	case "minimal":
		return shared.ReasoningEffortMinimal
	case "low":
		return shared.ReasoningEffortLow
	case "medium", "":
		return shared.ReasoningEffortMedium
	case "high":
		return shared.ReasoningEffortHigh
	case "xhigh", "extra_high", "extra-high":
		return shared.ReasoningEffortXhigh
	default:
		return shared.ReasoningEffortMedium
	}
}

func fromResponse(response *Response) ModelResponse {
	out := ModelResponse{
		ID:        response.ID,
		FinalText: response.OutputText(),
	}
	for _, item := range response.Output {
		if item.Type != "function_call" {
			continue
		}
		call := item.AsFunctionCall()
		args := strings.TrimSpace(call.Arguments)
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        firstNonEmpty(call.CallID, item.CallID, call.ID, item.ID),
			Name:      firstNonEmpty(call.Name, item.Name),
			Arguments: json.RawMessage(defaultJSON(args)),
		})
	}
	return out
}

func defaultJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func responseErrorMessage(response Response) string {
	if strings.TrimSpace(response.Error.Message) != "" {
		return response.Error.Message
	}
	if strings.TrimSpace(response.IncompleteDetails.Reason) != "" {
		return string(response.IncompleteDetails.Reason)
	}
	if strings.TrimSpace(string(response.Status)) != "" {
		return string(response.Status)
	}
	return "unknown error"
}
