package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

type agentModel struct {
	Name                  types.String   `tfsdk:"name"`
	Instructions          types.String   `tfsdk:"instructions"`
	Model                 types.String   `tfsdk:"model"`
	Harness               types.String   `tfsdk:"harness"`
	Trigger               []triggerModel `tfsdk:"trigger"`
	Tools                 *toolsModel    `tfsdk:"tools"`
	Sandbox               *sandboxModel  `tfsdk:"sandbox"`
	Budget                *budgetModel   `tfsdk:"budget"`
	KnowledgeBase         types.String   `tfsdk:"knowledge_base"`
	AllowedMessageSenders []string       `tfsdk:"allowed_message_senders"`
	Version               types.Int64    `tfsdk:"version"`
}

type triggerModel struct {
	Schedule types.String `tfsdk:"schedule"`
	Feed     types.String `tfsdk:"feed"`
	Webhook  types.String `tfsdk:"webhook"`
}

type toolsModel struct {
	MCP []mcpUseModel `tfsdk:"mcp"`
}

type mcpUseModel struct {
	Server types.String `tfsdk:"server"`
	As     types.String `tfsdk:"as"`
}

type sandboxModel struct {
	Image     types.String `tfsdk:"image"`
	Egress    []string     `tfsdk:"egress"`
	WebSearch types.Bool   `tfsdk:"web_search"`
}

type budgetModel struct {
	TokensPerSession types.Int64 `tfsdk:"tokens_per_session"`
	SessionsPerDay   types.Int64 `tfsdk:"sessions_per_day"`
	MaxTurns         types.Int64 `tfsdk:"max_turns"`
}

// spec is the agent JSON m says. A harness that is unset, or unknown
// until Glen answers, is left out, and Glen picks it.
func (m agentModel) spec() glen.AgentSpec {
	s := glen.AgentSpec{
		Agent:                 m.Name.ValueString(),
		Instructions:          m.Instructions.ValueString(),
		Harness:               m.Harness.ValueString(),
		Model:                 m.Model.ValueString(),
		KnowledgeBase:         m.KnowledgeBase.ValueStringPointer(),
		AllowedMessageSenders: m.AllowedMessageSenders,
	}
	for _, t := range m.Trigger {
		s.Trigger = append(s.Trigger, glen.Trigger{
			Schedule: t.Schedule.ValueStringPointer(),
			Feed:     t.Feed.ValueStringPointer(),
			Webhook:  t.Webhook.ValueStringPointer(),
		})
	}
	if m.Tools != nil {
		for _, u := range m.Tools.MCP {
			s.Tools.MCP = append(s.Tools.MCP, glen.McpUse{Server: u.Server.ValueString(), As: u.As.ValueString()})
		}
	}
	if m.Sandbox != nil {
		s.Sandbox = glen.Sandbox{Image: m.Sandbox.Image.ValueStringPointer(), Egress: m.Sandbox.Egress, WebSearch: m.Sandbox.WebSearch.ValueBool()}
	}
	if m.Budget != nil {
		s.Budget = glen.Budget{
			TokensPerSession: m.Budget.TokensPerSession.ValueInt64Pointer(),
			SessionsPerDay:   m.Budget.SessionsPerDay.ValueInt64Pointer(),
			MaxTurns:         m.Budget.MaxTurns.ValueInt64Pointer(),
		}
	}
	return s.Normalized()
}

// canonicalModel is a as a configuration that says no more than it must
// would read: empty lists, web search off and empty blocks are unset.
func canonicalModel(a glen.Agent) agentModel {
	s := a.Spec
	m := agentModel{
		Name:                  types.StringValue(s.Agent),
		Instructions:          types.StringValue(s.Instructions),
		Model:                 types.StringValue(s.Model),
		Harness:               types.StringValue(s.Harness),
		KnowledgeBase:         types.StringPointerValue(s.KnowledgeBase),
		AllowedMessageSenders: nonEmpty(s.AllowedMessageSenders),
		Version:               types.Int64Value(a.Version),
	}
	for _, t := range s.Trigger {
		m.Trigger = append(m.Trigger, triggerModel{
			Schedule: types.StringPointerValue(t.Schedule),
			Feed:     types.StringPointerValue(t.Feed),
			Webhook:  types.StringPointerValue(t.Webhook),
		})
	}
	if len(s.Tools.MCP) > 0 {
		m.Tools = &toolsModel{}
		for _, u := range s.Tools.MCP {
			as := types.StringNull()
			if u.As != "" {
				as = types.StringValue(u.As)
			}
			m.Tools.MCP = append(m.Tools.MCP, mcpUseModel{Server: types.StringValue(u.Server), As: as})
		}
	}
	if sb := s.Sandbox; sb.Image != nil || len(sb.Egress) > 0 || sb.WebSearch {
		m.Sandbox = &sandboxModel{Image: types.StringPointerValue(sb.Image), Egress: nonEmpty(sb.Egress), WebSearch: types.BoolNull()}
		if sb.WebSearch {
			m.Sandbox.WebSearch = types.BoolValue(true)
		}
	}
	if b := s.Budget; b.TokensPerSession != nil || b.SessionsPerDay != nil || b.MaxTurns != nil {
		m.Budget = &budgetModel{
			TokensPerSession: types.Int64PointerValue(b.TokensPerSession),
			SessionsPerDay:   types.Int64PointerValue(b.SessionsPerDay),
			MaxTurns:         types.Int64PointerValue(b.MaxTurns),
		}
	}
	return m
}

// refreshed keeps m, as written, while Glen holds the spec it says.
func (m agentModel) refreshed(a glen.Agent) agentModel {
	if m.spec().Equal(a.Spec) {
		m.Version = types.Int64Value(a.Version)
		return m
	}
	return canonicalModel(a)
}

func nonEmpty(xs []string) []string {
	if len(xs) == 0 {
		return nil
	}
	return xs
}
