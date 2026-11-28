package glen

import (
	"reflect"
	"strings"
)

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
	// AllowedMessageSenders are the roles whose members may message the
	// agent, allowing its held calls among those messages, besides its
	// admins. Left out when none, as Glen writes it.
	AllowedMessageSenders []string `json:"allowed_message_senders,omitempty"`
	// Notify is who Glen emails when the agent's session finishes, fails
	// or needs someone, and the Slack channel its asks and held calls are
	// posted to. Left out when unset, as Glen writes it.
	Notify *Notify `json:"notify,omitempty"`
}

type Notify struct {
	Email *EmailNotify `json:"email,omitempty"`
	Slack *SlackNotify `json:"slack,omitempty"`
}

// SlackNotify posts each of the agent's asks and held calls to Channel,
// a Slack channel's id, in the workspace the org added Glen's Slack app to.
type SlackNotify struct {
	Channel string `json:"channel"`
}

// EmailNotify emails the org's members To, by their sign-in address, on
// each of the events On: finished, failed and needs_you.
type EmailNotify struct {
	To []string `json:"to"`
	On []string `json:"on"`
}

// NotifyOn is every event an agent's notify may name.
var NotifyOn = []string{"finished", "failed", "needs_you"}

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
	if len(s.AllowedMessageSenders) == 0 {
		s.AllowedMessageSenders = nil
	}
	// A notify with no email and no Slack says nothing, and Glen leaves
	// it out.
	if s.Notify != nil && s.Notify.Email == nil && s.Notify.Slack == nil {
		s.Notify = nil
	}
	if s.Notify != nil {
		n := Notify{Slack: s.Notify.Slack}
		if s.Notify.Email != nil {
			e := *s.Notify.Email
			e.To, e.On = orEmpty(e.To), orEmpty(e.On)
			n.Email = &e
		}
		s.Notify = &n
	}
	return s
}

// Equal is whether s and o say the same agent. Glen matches
// notify.email.to to the org's members whatever the case, and keeps
// each as the member's own address.
func (s AgentSpec) Equal(o AgentSpec) bool {
	return reflect.DeepEqual(s.Normalized().foldedAddresses(), o.Normalized().foldedAddresses())
}

func (s AgentSpec) foldedAddresses() AgentSpec {
	if s.Notify == nil || s.Notify.Email == nil {
		return s
	}
	e := *s.Notify.Email
	e.To = make([]string, len(s.Notify.Email.To))
	for i, to := range s.Notify.Email.To {
		e.To[i] = strings.ToLower(to)
	}
	s.Notify = &Notify{Email: &e, Slack: s.Notify.Slack}
	return s
}

func orEmpty[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}
