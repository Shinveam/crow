package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"crow/internal/agent/llm"
	"crow/internal/agent/schema"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const finalFlag = "--end--"

type OpenAI struct {
	model       string
	maxTokens   int64
	temperature float64
	apiType     string // API的类型，例如 Azure、Aws、Openai
	apiKey      string
	apiVersion  string // Azure Openai version if AzureOpenai
	baseURL     string

	maxReties int // 最大重试次数
	// token计算相关属性
	totalInputTokens      int64
	totalCompletionTokens int64
	maxInputTokens        int64

	replyCh chan string
	lock    sync.Mutex
}

func NewOpenAI(model, apiKey, baseUrl string) *OpenAI {
	return &OpenAI{
		model:     model,
		maxTokens: 1000,
		apiKey:    apiKey,
		baseURL:   baseUrl,
		maxReties: 3,
		replyCh:   make(chan string, 10),
	}
}

func (o *OpenAI) Handle(ctx context.Context, request *llm.Request) (*llm.Response, error) {
	if request.ToolChoice == "" {
		request.ToolChoice = schema.ToolChoiceAuto
	} else {
		switch request.ToolChoice {
		case schema.ToolChoiceNone, schema.ToolChoiceAuto, schema.ToolChoiceRequired:
		default:
			return nil, fmt.Errorf("invalid tool_choice: %s", request.ToolChoice)
		}
	}

	if request.Timeout < 3*time.Second {
		request.Timeout = 300 * time.Second
	}

	var tools []openai.ChatCompletionToolParam
	for _, tool := range request.Tools {
		tools = append(tools, openai.ChatCompletionToolParam{
			Type: "function",
			Function: openai.FunctionDefinitionParam{
				Name:        tool.Function.Name,
				Description: openai.String(tool.Function.Description),
				Parameters:  tool.Function.Parameters,
			},
		})
	}

	formattedMessages, err := o.formatMessages(request.SystemMessage, request.Messages, request.IsSupportImages)
	if err != nil {
		return nil, fmt.Errorf("failed to format messages: %v", err)
	}

	client := openai.NewClient(
		option.WithBaseURL(o.baseURL),
		option.WithAPIKey(o.apiKey),
		option.WithMaxRetries(o.maxReties),
		option.WithRequestTimeout(request.Timeout),
	)
	stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:               o.model,
		Messages:            formattedMessages,
		Temperature:         openai.Float(o.temperature),
		MaxTokens:           openai.Int(o.maxTokens),
		MaxCompletionTokens: openai.Int(o.totalCompletionTokens),
		Tools:               tools,
		ToolChoice:          openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String(string(request.ToolChoice))},
	})
	// 累加器
	acc := openai.ChatCompletionAccumulator{}
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)

		// it's best to use chunks after handling JustFinished events
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			o.replyCh <- chunk.Choices[0].Delta.Content
		}
	}
	o.replyCh <- finalFlag

	if stream.Err() != nil {
		return nil, fmt.Errorf("stream error: %v", stream.Err())
	}
	if len(acc.Choices) == 0 {
		return nil, nil
	}

	resp := llm.Response{Content: acc.Choices[0].Message.Content}
	resp.ToolCalls = make([]schema.ToolCall, len(acc.Choices[0].Message.ToolCalls))
	for i, v := range acc.Choices[0].Message.ToolCalls {
		resp.ToolCalls[i] = schema.ToolCall{
			ID:   v.ID,
			Type: string(v.Type),
			Function: schema.ToolCallFunction{
				Name:      v.Function.Name,
				Arguments: v.Function.Arguments,
			},
		}
	}
	return &resp, nil
}

func (o *OpenAI) Recv() (string, error) {
	reply, ok := <-o.replyCh
	if !ok {
		return "", io.EOF
	}
	if reply == finalFlag {
		return "", io.EOF
	}
	return reply, nil
}

func (o *OpenAI) Reset() error {
	defer func() {
		_ = recover()
	}()
	close(o.replyCh)
	return nil
}

func (o *OpenAI) formatMessages(systemMessage schema.Message, messages []schema.Message, isSupportImage bool) ([]openai.ChatCompletionMessageParamUnion, error) {
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
