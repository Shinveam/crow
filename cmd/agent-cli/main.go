package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"crow/internal/agent"
	"crow/internal/agent/llm/openai"
	"crow/internal/agent/prompt"
	"crow/internal/agent/react"
	"crow/internal/config"
	log2 "crow/pkg/log"
	"crow/pkg/util"
)

func main() {
	cfg := config.NewConfig()
	if cfg == nil {
		panic("failed to load config")
	}

	agt := NewCLI(cfg)
	agt.InitAgent()

	var (
		userPrompt string
		chatRound  int
		isExit     bool
	)
	for {
		log.Println("input your query：")
		_, _ = fmt.Scanln(&userPrompt)
		userPrompt = util.RemoveAllPunctuation(strings.TrimSpace(userPrompt))
		for _, cmd := range cfg.CMDExit {
			if userPrompt == cmd {
				isExit = true
			}
		}

		chatRound++
		err := agt.agent.Run(context.Background(), userPrompt)
		if err != nil {
			log.Printf("chat round: %d\n%s\n", chatRound, err.Error())
		}

		<-agt.stop
		if isExit {
			break
		}
	}
}

type CLI struct {
	cfg   *config.Config
	agent agent.Provider
	reply string
	stop  chan struct{}
}

func NewCLI(cfg *config.Config) *CLI {
	return &CLI{
		cfg:  cfg,
		stop: make(chan struct{}, 1),
	}
}

func (c *CLI) InitAgent() {
	var llmCfg config.LLMConfig
	if v, ok := c.cfg.SelectedModule["llm"]; ok {
		if _, ok = c.cfg.LLM[v]; ok {
			llmCfg = c.cfg.LLM[v]
		}
	}
	llm := openai.NewOpenAI(llmCfg.Model, llmCfg.APIKey, llmCfg.BaseURL)
	mcpReAct, err := react.NewMCPAgent(context.Background(), nil)
	if err != nil {
		fmt.Printf("failed to create mcp agent: %v\n", err)
		return
	}

	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Properties  any    `json:"properties,omitempty"`
	}

	toolPrompt := ""
	toolDesc := "<tool>\n%s\n</tool>\n\n"
	for _, tool := range mcpReAct.GetTools() {
		info := toolInfo{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Properties:  tool.Function.Parameters["properties"],
		}
		jsonData, _ := json.Marshal(&info)
		toolPrompt += fmt.Sprintf(toolDesc, string(jsonData))
	}

	logger := log2.NewLogger(&log2.Option{
		Hook:        nil,
		Mode:        c.cfg.Server.Mode,
		ServiceName: "crow",
		EncodeType:  log2.EncodeTypeConsole,
	})
	c.agent = react.NewReActAgent("crow", logger, llm, mcpReAct,
		react.WithSystemPrompt(fmt.Sprintf(prompt.SystemPrompt, toolPrompt)),
		react.WithNextStepPrompt(prompt.NextStepPrompt),
		react.WithMaxObserve(500),
		react.WithMemoryMaxMessages(20))
	c.agent.SetListener(c)
}

func (c *CLI) OnAgentResult(ctx context.Context, text string, state agent.State) bool {
	if text == "" && state != agent.StateCompleted {
		return false
	}
	c.reply += text
	fmt.Printf("\r【Crow】: %s", c.reply)

	if state == agent.StateCompleted {
		c.reply = ""
		_ = c.agent.Reset()
		c.stop <- struct{}{}
		return true
	}
	return false
}
