package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"crow/internal/agent"
	"crow/internal/config"
	"crow/internal/llm/openai"
	"crow/internal/prompt"
	log2 "crow/pkg/log"
	"crow/pkg/util"
)

func main() {
	cfg := config.NewConfig()
	if cfg == nil {
		panic("failed to load config")
	}

	agt := initAgent(cfg)
	defer func() {
		agt.Reset()
	}()
	go onAgentResult(agt)

	var (
		userPrompt string
		chatRound  int
	)
	for {
		log.Println("input your query：")
		_, _ = fmt.Scanln(&userPrompt)
		userPrompt = util.RemoveAllPunctuation(strings.TrimSpace(userPrompt))
		for _, cmd := range cfg.CMDExit {
			if userPrompt == cmd {
				log.Println("Good bye!")
				return
			}
		}
		chatRound++
		err := agt.Run(context.Background(), userPrompt)
		if err != nil {
			log.Printf("chat round: %d\n%s\n", chatRound, err.Error())
		}
	}
}

func initAgent(cfg *config.Config) *agent.Agent {
	var llmCfg config.LLMConfig
	if v, ok := cfg.SelectedModule["llm"]; ok {
		if _, ok = cfg.LLM[v]; ok {
			llmCfg = cfg.LLM[v]
		}
	}
	llmClient := openai.NewLLM(llmCfg.Model, llmCfg.APIKey, llmCfg.BaseURL, true)
	mcpReAct, err := agent.NewMCPAgent(context.Background())
	if err != nil {
		panic(fmt.Errorf("failed to create mcp agent: %v", err))
	}
	morePrompt := ""
	for _, tool := range mcpReAct.GetTools() {
		morePrompt += fmt.Sprintf("#### %s\n", tool.Function.Name)
		if tool.Function.Description != "" {
			morePrompt += fmt.Sprintf("* 描述: %s\n", tool.Function.Description)
		}
		if properties, ok := tool.Function.Parameters["properties"].(map[string]any); ok {
			var params string
			for k, v := range properties {
				if v, ok := v.(map[string]any); ok && v["description"] != nil {
					params += fmt.Sprintf("    - %s: %s\n", k, v["description"])
					continue
				} else {
					params += fmt.Sprintf("    - %s\n", k)
				}
			}
			if params != "" {
				morePrompt += fmt.Sprintf("* 参数:\n%s\n", params)
			}
		}
	}
	logger := log2.NewLogger(&log2.Option{
		Hook:        nil,
		Mode:        cfg.Server.Mode,
		ServiceName: "crow",
		EncodeType:  log2.EncodeTypeConsole,
	})
	return agent.NewAgent("crow", logger, llmClient, mcpReAct,
		agent.WithSystemPrompt(prompt.SystemPrompt+morePrompt),
		agent.WithNextStepPrompt(prompt.NextStepPrompt),
		agent.WithMaxObserve(500),
		agent.WithMemoryMaxMessages(50),
	)
}

func onAgentResult(agt *agent.Agent) {
	replyText := ""
	for {
		text, ok := agt.GetStreamReplyText()
		if !ok {
			break
		}
		if agt.IsFinalFlag(text) {
			replyText = ""
			fmt.Println()
			continue
		}
		replyText += text
		fmt.Printf("\r【Crow】: %s", replyText)
	}
	fmt.Println()
}
