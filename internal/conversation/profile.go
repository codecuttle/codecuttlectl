package conversation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/swarm"
)

// NodeProfile is the fully resolved runtime identity of one swarm node: which
// provider answers, which persona is rendered, and which tools are authorized.
// It is a value snapshot; callers must not treat it as live Agent state.
type NodeProfile struct {
	NodeID       string
	Provider     provider.Provider
	ProviderName string // morphology provider kind ("bedrock", "openrouter"...)
	Model        string
	Workbench    []string
	SystemPrompt string
}

// RouteAllowed reports whether a handoff from source to target is permitted by
// the morphology topology. With no rules at all, legacy morphologies allow any
// defined target. If any rules exist, a missing source entry denies routing.
func RouteAllowed(morph *swarm.Morphology, source, target string) bool {
	if morph == nil {
		return false
	}
	if _, ok := morph.Nodes[target]; !ok {
		return false
	}
	if len(morph.Topology.Rules) == 0 {
		return true
	}
	for _, allowed := range morph.Topology.Rules[source] {
		if allowed == target {
			return true
		}
	}
	return false
}

// ProviderNameFor derives the morphology provider kind from a provider ID
// prefix (e.g. "openrouter:model" -> "openrouter"). Unknown formats return "".
func ProviderNameFor(p provider.Provider) string {
	if p == nil {
		return ""
	}
	id := p.ID()
	if i := strings.IndexByte(id, ':'); i > 0 {
		return id[:i]
	}
	return ""
}

// ResolveNodeProfile builds the profile for nodeID without mutating any state.
// It validates the node exists, the pool can supply its provider, and renders
// the system prompt with the node's authorized catalog and persona.
func ResolveNodeProfile(morph *swarm.Morphology, pool provider.Pool, pluginMgr *pluginhost.Manager, promptMgr *prompt.Manager, workDir, nodeID string) (*NodeProfile, error) {
	if morph == nil {
		return nil, fmt.Errorf("morphology is not enabled")
	}
	node, ok := morph.Nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %q does not exist in the active morphology", nodeID)
	}
	if pool == nil {
		return nil, fmt.Errorf("no provider pool is configured for node %q", nodeID)
	}
	prov, ok := pool.GetNode(nodeID)
	if !ok || prov == nil {
		return nil, fmt.Errorf("provider for node %q failed to initialize", nodeID)
	}

	profile := &NodeProfile{
		NodeID:       nodeID,
		Provider:     prov,
		ProviderName: node.Provider,
		Model:        node.Model,
		Workbench:    append([]string(nil), node.Workbench...),
	}

	if promptMgr != nil {
		var promptTools []prompt.ToolDef
		for _, def := range allToolDefs(pluginMgr, profile.Workbench, morph) {
			promptTools = append(promptTools, prompt.ToolDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  prompt.SchemaToToolParams(def.InputSchema),
			})
		}
		var peers []string
		for id := range morph.Nodes {
			if id != nodeID {
				peers = append(peers, id)
			}
		}
		sort.Strings(peers)
		rendered, err := promptMgr.RenderSystem(workDir, node.Model, node.Provider, promptTools, peers)
		if err != nil {
			return nil, fmt.Errorf("rendering system prompt for %q: %w", nodeID, err)
		}
		if pluginMgr != nil {
			if hints := pluginMgr.LLMHints(); hints != "" {
				rendered += "\n\n## Additional Tool Guidance\n" + hints
			}
		}
		if node.SystemPrompt != "" {
			rendered += "\n\n## Persona Instructions\n" + node.SystemPrompt
		}
		profile.SystemPrompt = rendered
	} else if node.SystemPrompt != "" {
		profile.SystemPrompt = node.SystemPrompt
	}
	return profile, nil
}

// PrimaryNodeID returns the primary node of a morphology, or "" if none.
func PrimaryNodeID(morph *swarm.Morphology) string {
	if morph == nil {
		return ""
	}
	for id, node := range morph.Nodes {
		if node.IsPrimary {
			return id
		}
	}
	return ""
}

// ProviderToolDefs converts the Bedrock-shaped catalog to provider definitions.
func ProviderToolDefs(pluginMgr *pluginhost.Manager, workbench []string, morph *swarm.Morphology) []provider.ToolDefinition {
	var out []provider.ToolDefinition
	for _, def := range allToolDefs(pluginMgr, workbench, morph) {
		out = append(out, provider.ToolDefinition{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: def.InputSchema,
		})
	}
	return out
}

// Profile returns the Agent's current node identity as a value snapshot.
func (a *Agent) Profile() NodeProfile {
	p := NodeProfile{
		NodeID:       a.activeNode,
		Provider:     a.provider,
		ProviderName: ProviderNameFor(a.provider),
		Workbench:    append([]string(nil), a.workbench...),
		SystemPrompt: a.systemPrompt,
	}
	if a.morph != nil {
		if node, ok := a.morph.Nodes[a.activeNode]; ok {
			p.ProviderName = node.Provider
			p.Model = node.Model
		}
	}
	return p
}

// ProviderToolDefs returns the Agent's currently authorized catalog for requests.
func (a *Agent) ProviderToolDefs() []provider.ToolDefinition {
	return ProviderToolDefs(a.pluginMgr, a.workbench, a.morph)
}

// applyProfile atomically switches the Agent to an already-resolved profile.
// Callers must have validated the profile first; this never fails midway.
func (a *Agent) applyProfile(p *NodeProfile, sanitizedHistory []provider.Message) {
	a.activeNode = p.NodeID
	a.provider = p.Provider
	a.client = nil // provider interface only after a swarm switch
	a.workbench = append([]string(nil), p.Workbench...)
	if p.SystemPrompt != "" {
		a.systemPrompt = p.SystemPrompt
	}
	if sanitizedHistory != nil {
		a.provHistory = sanitizedHistory
	}
}

// handoffPayload is the wire shape of the native handoff tool.
type handoffPayload struct {
	Target       string `json:"target"`
	Instructions string `json:"instructions"`
}

func parseHandoff(input json.RawMessage) (handoffPayload, error) {
	var p handoffPayload
	if err := json.Unmarshal(input, &p); err != nil {
		return p, fmt.Errorf("parsing handoff input: %w", err)
	}
	if strings.TrimSpace(p.Target) == "" {
		return p, fmt.Errorf("handoff target is required")
	}
	return p, nil
}
