package react

import (
	"time"

	"crow/internal/agent/memory"
)

type Option func(agent *ReActAgent)

func WithAgentDescription(description string) Option {
	return func(agent *ReActAgent) {
		agent.description = description
	}
}

func WithSystemPrompt(systemPrompt string) Option {
	return func(agent *ReActAgent) {
		agent.systemPrompt = systemPrompt
	}
}

func WithNextStepPrompt(nextStepPrompt string) Option {
	return func(agent *ReActAgent) {
		agent.nextStepPrompt = nextStepPrompt
	}
}

func WithMaxSteps(maxSteps int) Option {
	return func(agent *ReActAgent) {
		if maxSteps > 0 {
			agent.maxSteps = maxSteps
		} else {
			agent.maxSteps = 10
		}
	}
}

func WithMaxObserve(maxObserve int) Option {
	return func(agent *ReActAgent) {
		if maxObserve > 0 {
			agent.maxObserve = maxObserve
		}
	}
}

func WithPeerAskTimeout(timeout time.Duration) Option {
	return func(agent *ReActAgent) {
		agent.peerAskTimeout = timeout
	}
}

func WithDuplicateThreshold(duplicateThreshold int) Option {
	return func(agent *ReActAgent) {
		if duplicateThreshold > 0 {
			agent.duplicateThreshold = duplicateThreshold
		} else {
			agent.duplicateThreshold = 2
		}
	}
}

func WithMemoryMaxMessages(maxMessages int) Option {
	return func(agent *ReActAgent) {
		if maxMessages > 0 {
			agent.memory = memory.NewDefaultMemory(maxMessages)
		}
	}
}

func WithSupportImages(supportImages bool) Option {
	return func(agent *ReActAgent) {
		agent.supportImages = supportImages
	}
}
