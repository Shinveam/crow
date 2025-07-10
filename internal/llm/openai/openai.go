package openai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"crow/internal/llm"
	"crow/internal/schema"
)

const FinalFlag = "--end--"

type LLM struct {
	Model       string
	MaxTokens   int64
	Temperature float64
	APIType     string // API的类型，例如 Azure、Aws、Openai
	APIKey      string
	APIVersion  string // Azure Openai version if AzureOpenai
	BaseURL     string
	// 最大重试次数
	MaxReties int
	// 启用流式输出
	EnableStream bool
	// token计算相关属性
	TotalInputTokens      int64
	TotalCompletionTokens int64
	MaxInputTokens        int64
	// streamReplyTextCh 流式响应文本管道
	// 当启用流式输出时，会将响应文本写入管道
	streamReplyTextCh chan string
}

func NewLLM(model, apiKey, baseUrl string, enableStream bool) *LLM {
	var streamCh chan string
	if enableStream {
		streamCh = make(chan string)
	}
	return &LLM{
		Model:             model,
		APIKey:            apiKey,
		BaseURL:           baseUrl,
		MaxReties:         3,
		EnableStream:      enableStream,
		streamReplyTextCh: streamCh,
	}
}

func (l *LLM) GetStreamReplyText() (string, bool) {
	if !l.EnableStream {
		return "", false
	}
	text, ok := <-l.streamReplyTextCh
	return text, ok
}

func (l *LLM) formatMessages(systemMessage schema.Message, messages []schema.Message, isSupportImage bool) ([]openai.ChatCompletionMessageParamUnion, error) {
	var formattedMessages []openai.ChatCompletionMessageParamUnion
	if systemMessage.Content != "" {
		formattedMessages = make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
		formattedMessages = append(formattedMessages, openai.SystemMessage(systemMessage.Content))
	} else {
		formattedMessages = make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	}

	isIncludeUserMessage := false
	for i, msg := range messages {
		switch msg.Role {
		case schema.RoleUser:
			isIncludeUserMessage = true
			ofArrayOfContentParts := []openai.ChatCompletionContentPartUnionParam{
				{OfText: &openai.ChatCompletionContentPartTextParam{Text: msg.Content}},
			}
			if isSupportImage && msg.Base64Image != "" {
				imageUri := msg.Base64Image
				if !strings.HasPrefix(msg.Base64Image, "data:image/jpeg;base64,") && !strings.HasPrefix(msg.Base64Image, "http") {
					imageUri = fmt.Sprintf("data:image/jpeg;base64,%s", msg.Base64Image)
				}
				OfImageURL := openai.ChatCompletionContentPartUnionParam{
					OfImageURL: &openai.ChatCompletionContentPartImageParam{
						ImageURL: openai.ChatCompletionContentPartImageImageURLParam{URL: imageUri},
					},
				}
				ofArrayOfContentParts = append(ofArrayOfContentParts, OfImageURL)
			}
			formattedMessages = append(formattedMessages, openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{OfArrayOfContentParts: ofArrayOfContentParts},
				},
			})
			messages[i].Base64Image = "" // 清空base64图片，避免占用内存
		case schema.RoleAssistant:
			ofAsst := &openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(msg.Content),
				},
			}
			for _, toolCall := range msg.ToolCalls {
				ofAsst.ToolCalls = append(ofAsst.ToolCalls, openai.ChatCompletionMessageToolCallParam{
					ID: toolCall.ID,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name:      toolCall.Function.Name,
						Arguments: toolCall.Function.Arguments,
					},
				})
			}
			formattedMessages = append(formattedMessages, openai.ChatCompletionMessageParamUnion{OfAssistant: ofAsst})
		case schema.RoleTool:
			formattedMessages = append(formattedMessages, openai.ToolMessage(msg.Content, msg.ToolCallID))
		default:
			return nil, errors.New("invalid role")
		}
	}
	if !isIncludeUserMessage {
		return nil, errors.New("messages must contain 'role' field")
	}
	return formattedMessages, nil
}

func (l *LLM) Ask(ctx context.Context, params llm.AskRequestParams) (string, error) {
	formattedMessages, err := l.formatMessages(params.SystemMessage, params.Messages, params.IsSupportImages)
	if err != nil {
		return "", fmt.Errorf("failed to format messages: %v", err)
	}

	client := openai.NewClient(
		option.WithBaseURL(l.BaseURL),
		option.WithAPIKey(l.APIKey),
		option.WithMaxRetries(l.MaxReties),
	)
	// 非流式输出
	if !l.EnableStream {
		var response *openai.ChatCompletion
		response, err = client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:               l.Model,
			Messages:            formattedMessages,
			Temperature:         openai.Float(l.Temperature),
			MaxTokens:           openai.Int(l.MaxTokens),
			MaxCompletionTokens: openai.Int(l.TotalCompletionTokens),
		})
		if err != nil {
			return "", fmt.Errorf("failed to ask: %v", err)
		}
		if len(response.Choices) == 0 {
			return "", errors.New("unexpected errors in ask")
		}
		if l.streamReplyTextCh != nil {
			l.streamReplyTextCh <- response.Choices[0].Message.Content
		}
		return response.Choices[0].Message.Content, nil
	}
	// 流式输出
	stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:               l.Model,
		Messages:            formattedMessages,
		Temperature:         openai.Float(l.Temperature),
		MaxTokens:           openai.Int(l.MaxTokens),
		MaxCompletionTokens: openai.Int(l.TotalCompletionTokens),
	})
	// 累加器
	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		if _, ok := acc.JustFinishedContent(); ok {
			if l.streamReplyTextCh != nil {
				l.streamReplyTextCh <- FinalFlag
			}
		}

		if refusal, ok := acc.JustFinishedRefusal(); ok {
			return "", fmt.Errorf("refusal: %s", refusal)
		}

		// it's best to use chunks after handling JustFinished events
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			if l.streamReplyTextCh != nil {
				l.streamReplyTextCh <- chunk.Choices[0].Delta.Content
			}
		}
	}
	if stream.Err() != nil {
		return "", fmt.Errorf("stream error: %v", stream.Err())
	}
	if len(acc.Choices) == 0 {
		return "", errors.New("unexpected errors in ask")
	}
	return acc.Choices[0].Message.Content, nil
}

func (l *LLM) AskTool(ctx context.Context, params llm.AskToolRequestParams) (*llm.AskToolResponse, error) {
	if params.ToolChoice == "" {
		params.ToolChoice = schema.ToolChoiceAuto
	} else {
		switch params.ToolChoice {
		case schema.ToolChoiceNone, schema.ToolChoiceAuto, schema.ToolChoiceRequired:
		default:
			return nil, fmt.Errorf("invalid tool_choice: %s", params.ToolChoice)
		}
	}

	if params.Timeout <= 0 {
		params.Timeout = 300 * time.Second
	}

	var toolCalls []openai.ChatCompletionToolParam
	for _, tool := range params.Tools {
		if tool.Type == "" {
			return nil, errors.New("each tool must be a dict with 'type' field")
		}
		toolCalls = append(toolCalls, openai.ChatCompletionToolParam{
			Type: "function",
			Function: openai.FunctionDefinitionParam{
				Name:        tool.Function.Name,
				Description: openai.String(tool.Function.Description),
				Parameters:  tool.Function.Parameters,
			},
		})
	}

	formattedMessages, err := l.formatMessages(params.SystemMessage, params.Messages, params.IsSupportImages)
	if err != nil {
		return nil, fmt.Errorf("failed to format messages: %v", err)
	}

	client := openai.NewClient(
		option.WithBaseURL(l.BaseURL),
		option.WithAPIKey(l.APIKey),
		option.WithMaxRetries(l.MaxReties),
		option.WithRequestTimeout(params.Timeout),
	)
	// 非流式输出
	if !l.EnableStream {
		var response *openai.ChatCompletion
		response, err = client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:               l.Model,
			Messages:            formattedMessages,
			Temperature:         openai.Float(l.Temperature),
			MaxTokens:           openai.Int(l.MaxTokens),
			MaxCompletionTokens: openai.Int(l.TotalCompletionTokens),
			Tools:               toolCalls,
			ToolChoice:          openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String(string(params.ToolChoice))},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to ask tool: %v", err)
		}
		if len(response.Choices) == 0 {
			return nil, nil
		}

		if l.streamReplyTextCh != nil {
			l.streamReplyTextCh <- response.Choices[0].Message.Content
		}

		askToolResponse := llm.AskToolResponse{Content: response.Choices[0].Message.Content}
		askToolResponse.ToolCalls = make([]schema.ToolCall, len(response.Choices[0].Message.ToolCalls))
		for i, v := range response.Choices[0].Message.ToolCalls {
			askToolResponse.ToolCalls[i] = schema.ToolCall{
				ID:   v.ID,
				Type: string(v.Type),
				Function: schema.ToolCallFunction{
					Name:      v.Function.Name,
					Arguments: v.Function.Arguments,
				},
			}
		}
		return &askToolResponse, nil
	}
	// 流式输出
	stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:               l.Model,
		Messages:            formattedMessages,
		Temperature:         openai.Float(l.Temperature),
		MaxTokens:           openai.Int(l.MaxTokens),
		MaxCompletionTokens: openai.Int(l.TotalCompletionTokens),
		Tools:               toolCalls,
		ToolChoice:          openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String(string(params.ToolChoice))},
	})
	// 累加器
	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		if _, ok := acc.JustFinishedContent(); ok {
			if l.streamReplyTextCh != nil {
				l.streamReplyTextCh <- FinalFlag
			}
		}

		// if using tool calls
		if _, ok := acc.JustFinishedToolCall(); ok {
			if l.streamReplyTextCh != nil {
				l.streamReplyTextCh <- FinalFlag
			}
			// log.Printf("Tool call stream finished: %v, %v, %v\n", tool.Index, tool.Name, tool.Arguments)
		}

		if refusal, ok := acc.JustFinishedRefusal(); ok {
			return nil, fmt.Errorf("refusal: %s", refusal)
		}

		// it's best to use chunks after handling JustFinished events
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			if l.streamReplyTextCh != nil {
				l.streamReplyTextCh <- chunk.Choices[0].Delta.Content
			}
		}
	}
	if stream.Err() != nil {
		return nil, fmt.Errorf("stream error: %v", stream.Err())
	}
	if len(acc.Choices) == 0 {
		return nil, nil
	}

	askToolResponse := llm.AskToolResponse{Content: acc.Choices[0].Message.Content}
	askToolResponse.ToolCalls = make([]schema.ToolCall, len(acc.Choices[0].Message.ToolCalls))
	for i, v := range acc.Choices[0].Message.ToolCalls {
		askToolResponse.ToolCalls[i] = schema.ToolCall{
			ID:   v.ID,
			Type: string(v.Type),
			Function: schema.ToolCallFunction{
				Name:      v.Function.Name,
				Arguments: v.Function.Arguments,
			},
		}
	}
	return &askToolResponse, nil
}

func (l *LLM) IsFinalFlag(text string) bool {
	return text == FinalFlag
}

func (l *LLM) Cleanup() {
	defer func() {
		_ = recover()
	}()
	if l.streamReplyTextCh == nil {
		return
	}
	close(l.streamReplyTextCh)
}
