package glen

import "reflect"

type AgentSpec struct {
	Agent        string `json:"agent"`
	Instructions string `json:"instructions"`
	// Harness is left out when empty, and Glen picks it.
	Harness       string    `json:"harness,omitempty"`
	Model         string    `json:"model"`
	Trigger       []Trigger `json:"trigger"`
	Tools         Tools     `json:"tools"`
	Sandbox       Sandbox   `json:"sandbox"`
	Budget        Budget    `json:"budget"`
	KnowledgeBase *string   `json:"knowledge_base"`
	// Approvers are the roles whose members may allow the agent's held
	// calls, besides its admins. Left out when none, as Glen writes it.
	Approvers []string `json:"approvers,omitempty"`
}

type Trigger struct {
	Schedule *string `json:"schedule,omitempty"`
	Feed     *string `json:"feed,omitempty"`
	Webhook  *string `json:"webhook,omitempty"`
}

type Tools struct {
	MCP []McpUse `json:"mcp"`
}

type Sandbox struct {
	Image  *string  `json:"image"`
	Egress []string `json:"egress"`
	// WebSearch is left out when off, as Glen writes it.
	WebSearch bool `json:"web_search,omitempty"`
}

type Budget struct {
	TokensPerSession *int64 `json:"tokens_per_session"`
	SessionsPerDay   *int64 `json:"sessions_per_day"`
	MaxTurns         *int64 `json:"max_turns"`
}

type Agent struct {
	Version int64     `json:"version"`
	Spec    AgentSpec `json:"spec"`
}

type KnowledgeBaseSpec struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Guidance    *string `json:"guidance"`
	Curator     Curator `json:"curator"`
}

type Curator struct {
	Model        string `json:"model"`
	TokensPerDay int64  `json:"tokens_per_day"`
}

type KnowledgeBase struct {
	Version int64             `json:"version"`
	Spec    KnowledgeBaseSpec `json:"spec"`
}

func (s AgentSpec) Normalized() AgentSpec {
	s.Trigger = orEmpty(s.Trigger)
	s.Tools.MCP = normalizedUses(s.Tools.MCP)
	s.Sandbox.Egress = orEmpty(s.Sandbox.Egress)
	if len(s.Approvers) == 0 {
		s.Approvers = nil
	}
	return s
}

func (s AgentSpec) Equal(o AgentSpec) bool {
	return reflect.DeepEqual(s.Normalized(), o.Normalized())
}

func orEmpty[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}
