package agent

import (
	"time"

	"crow/internal/schema"
)

type Option func(agent *Agent)

func WithAgentDescription(description string) Option {
	return func(agent *Agent) {
		agent.description = description
	}
}

func WithSystemPrompt(systemPrompt string) Option {
	return func(agent *Agent) {
		agent.systemPrompt = systemPrompt
	}
}

func WithNextStepPrompt(nextStepPrompt string) Option {
	return func(agent *Agent) {
		agent.nextStepPrompt = nextStepPrompt
	}
}

func WithMaxSteps(maxSteps int) Option {
	return func(agent *Agent) {
		if maxSteps > 0 {
			agent.maxSteps = maxSteps
		} else {
			agent.maxSteps = 10
		}
	}
}

func WithMaxObserve(maxObserve int) Option {
	return func(agent *Agent) {
		if maxObserve > 0 {
			agent.maxObserve = maxObserve
		}
	}
}

func WithPeerAskTimeout(timeout time.Duration) Option {
	return func(agent *Agent) {
		agent.peerAskTimeout = timeout
	}
}

func WithDuplicateThreshold(duplicateThreshold int) Option {
	return func(agent *Agent) {
		if duplicateThreshold > 0 {
			agent.duplicateThreshold = duplicateThreshold
		} else {
			agent.duplicateThreshold = 2
		}
	}
}

func WithMemoryMaxMessages(maxMessages int) Option {
	return func(agent *Agent) {
		if maxMessages > 0 {
			agent.memory = schema.NewMemory(maxMessages)
		}
	}
}

func WithSupportImages(supportImages bool) Option {
	return func(agent *Agent) {
		agent.supportImages = supportImages
	}
}
