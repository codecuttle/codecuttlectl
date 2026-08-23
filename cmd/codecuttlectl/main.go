// codecuttlectl is the Codecuttle meta-harness CLI agent.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/codecuttle/codecuttlectl/internal/audit"
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/conversation"
	"github.com/codecuttle/codecuttlectl/internal/keyring"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	bedrockprov "github.com/codecuttle/codecuttlectl/internal/provider/bedrock"
	googleprov "github.com/codecuttle/codecuttlectl/internal/provider/google"
	"github.com/codecuttle/codecuttlectl/internal/provider/ollama"
	"github.com/codecuttle/codecuttlectl/internal/provider/openrouter"
	"github.com/codecuttle/codecuttlectl/internal/session"
	"github.com/codecuttle/codecuttlectl/internal/swarm"
	"github.com/codecuttle/codecuttlectl/internal/tui"
)

func main() {
	var (
		modelID              = flag.String("model", "us.anthropic.claude-opus-4-6-v1", "Bedrock model ID or alias (opus, sonnet, haiku, or model name when --provider is set)")
		auxModel             = flag.String("aux-model", "", "Auxiliary model for summaries/titles (transitional, will be superseded by --morph)")
		planModel            = flag.String("plan-model", "", "Planning model for mid-tier tasks (transitional, will be superseded by --morph)")
		region               = flag.String("region", "", "AWS region (default: AWS_REGION env or us-west-2)")
		profile              = flag.String("profile", "", "AWS profile name")
		providerF            = flag.String("provider", "", "LLM provider: 'bedrock' (default), 'google', 'ollama', or 'openrouter'")
		ollamaURL            = flag.String("ollama-url", "", "Ollama server URL (default: http://localhost:11434)")
		openrouterURL        = flag.String("openrouter-url", "", "OpenRouter server URL (default: https://openrouter.ai/api/v1)")
		openrouterZDR        = flag.Bool("openrouter-zdr", true, "Enable Zero Data Retention (ZDR) on OpenRouter requests (default true, use --openrouter-zdr=false to disable)")
		openrouterFallbacks  = flag.String("openrouter-fallbacks", "", "Comma-separated list of fallback models for OpenRouter")
		googleCacheThreshold = flag.Int("google-cache-threshold", 32000, "Token threshold to trigger Google Context Caching API (default 32000)")
		morphPath            = flag.String("morph", "", "Path to a Swarm Morphology YAML file (overrides model/provider flags)")
		workDir              = flag.String("workdir", "", "Working directory (default: current directory)")
		pluginDir            = flag.String("plugin-dir", "", "Directory containing Cuttlebone plugin binaries")
		verbose              = flag.Bool("verbose", false, "Enable verbose/debug output")
		maxSteps             = flag.Int("max-steps", 25, "Maximum tool-use iterations per turn")
		thinking             = flag.Bool("thinking", false, "Enable extended thinking/reasoning (model must support it)")
		oneShot              = flag.String("message", "", "Single message mode: send this message and exit (no TUI)")
		noTUI                = flag.Bool("no-tui", false, "Disable TUI, use plain REPL mode")

		// Session management
		sessionID     = flag.String("session", "", "Resume an existing session by ID")
		listSessions  = flag.Bool("list-sessions", false, "Show recent sessions and exit")
		listModels    = flag.Bool("list-models", false, "List available models for the selected provider and exit")
		sessionLimit  = flag.Int("session-limit", 20, "Number of sessions to show in list")
		pruneSessions = flag.Int("prune-sessions", 0, "Delete sessions older than N days and exit")

		// Safety
		autoApprove = flag.Bool("auto-approve", false, "Skip confirmation prompts for destructive operations (use in automated pipelines)")
		auditLog    = flag.Bool("audit-log", false, "Emit structured JSON audit events to stderr")
	)
	flag.Parse()

	// Initialize audit logger
	auditLogger := audit.NewLogger(os.Stderr, *auditLog)

	// Resolve working directory
	if *workDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
			os.Exit(1)
		}
		*workDir = wd
	}

	// Resolve region from env if not specified
	if *region == "" {
		*region = os.Getenv("AWS_REGION")
		if *region == "" {
			*region = os.Getenv("AWS_DEFAULT_REGION")
		}
	}

	// Resolve profile from env if not specified
	if *profile == "" {
		*profile = os.Getenv("AWS_PROFILE")
	}

	// Initialize session store
	store, err := session.NewFileStore(session.DefaultDataDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing session store: %v\n", err)
		os.Exit(1)
	}

	// Handle --list-sessions
	if *listSessions {
		runListSessions(store, *sessionLimit)
		return
	}

	// Handle --list-models
	if *listModels {
		runListModels(*providerF, *modelID)
		return
	}

	// Handle --prune-sessions
	if *pruneSessions > 0 {
		runPruneSessions(store, *pruneSessions)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Initialize LLM provider
	var llmProvider provider.Provider
	var bedrockClient *bedrock.Client // Non-nil only for Bedrock (needed for Bedrock-specific features)
	var genericPool provider.Pool
	var morph *swarm.Morphology
	providerName := *providerF

	if *morphPath != "" {
		var err error
		morph, err = swarm.LoadMorphology(*morphPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading morphology: %v\n", err)
			os.Exit(1)
		}

		factory := func(ctx context.Context, provName, modID string) (provider.Provider, error) {
			switch provName {
			case "ollama":
				return ollama.New(ollama.Config{
					BaseURL: *ollamaURL,
					Model:   modID,
				}), nil
			case "openrouter":
				if err := keyring.EnsureOpenRouterAPIKey(); err != nil {
					return nil, fmt.Errorf("failed to ensure OpenRouter API key: %w", err)
				}
				var fallbacks []string
				if *openrouterFallbacks != "" {
					for _, f := range strings.Split(*openrouterFallbacks, ",") {
						fallbacks = append(fallbacks, strings.TrimSpace(f))
					}
				}
				return openrouter.New(openrouter.Config{
					BaseURL:    *openrouterURL,
					Model:      modID,
					Fallbacks:  fallbacks,
					EnforceZDR: *openrouterZDR,
					APIKey:     os.Getenv("OPENROUTER_API_KEY"),
				}), nil
			case "google":
				if err := keyring.EnsureGeminiAPIKey(); err != nil {
					return nil, fmt.Errorf("failed to ensure Gemini API key: %w", err)
				}
				client, err := googleprov.New(ctx, googleprov.Config{
					Model:          modID,
					CacheThreshold: *googleCacheThreshold,
				})
				if err == nil {
					// Add cleanup? (omitted for simple factory)
				}
				return client, err
			case "bedrock":
				// Create single bedrock client (could optimize by caching session)
				resolved := bedrock.ResolveModelID(modID)
				c, err := bedrock.NewClient(ctx, bedrock.Config{
					Region:  *region,
					Profile: *profile,
					ModelID: resolved,
				})
				if err != nil {
					return nil, err
				}
				return bedrockprov.New(c), nil
			default:
				return nil, fmt.Errorf("unsupported provider: %s", provName)
			}
		}

		pool, err := swarm.NewPool(ctx, morph, factory)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing swarm pool: %v\n", err)
			os.Exit(1)
		}

		genericPool = pool

		// Map genericPool to legacy components for smooth transition
		llmProvider = pool.Primary()

		if *verbose {
			fmt.Fprintf(os.Stderr, "[swarm] morphology loaded: %s (%s)\n", morph.Name, morph.Presentation)
			fmt.Fprintf(os.Stderr, "[swarm] primary node=%s\n", pool.Info("primary").DisplayName)
		}
	} else {
		// Auto-detect provider from model name prefix (e.g., "ollama:gemma4")
		if providerName == "" && strings.HasPrefix(*modelID, "ollama:") {
			providerName = "ollama"
			*modelID = strings.TrimPrefix(*modelID, "ollama:")
		} else if providerName == "" && strings.HasPrefix(*modelID, "openrouter:") {
			providerName = "openrouter"
			*modelID = strings.TrimPrefix(*modelID, "openrouter:")
		}
		if providerName == "" {
			providerName = "bedrock"
		}

		switch providerName {
		case "ollama":
			ollamaClient := ollama.New(ollama.Config{
				BaseURL: *ollamaURL,
				Model:   *modelID,
			})
			llmProvider = ollamaClient
			if *verbose {
				fmt.Fprintf(os.Stderr, "[provider] ollama model=%s url=%s\n", *modelID, ollamaClient.ID())
			}

		case "openrouter":
			if err := keyring.EnsureOpenRouterAPIKey(); err != nil {
				fmt.Fprintf(os.Stderr, "Error ensuring OpenRouter API key: %v\n", err)
				os.Exit(1)
			}
			var fallbacks []string
			if *openrouterFallbacks != "" {
				for _, f := range strings.Split(*openrouterFallbacks, ",") {
					fallbacks = append(fallbacks, strings.TrimSpace(f))
				}
			}
			orClient := openrouter.New(openrouter.Config{
				BaseURL:    *openrouterURL,
				Model:      *modelID,
				Fallbacks:  fallbacks,
				EnforceZDR: *openrouterZDR,
				APIKey:     os.Getenv("OPENROUTER_API_KEY"),
			})
			llmProvider = orClient
			if *verbose {
				fmt.Fprintf(os.Stderr, "[provider] openrouter model=%s fallbacks=%v zdr=%v\n", *modelID, fallbacks, *openrouterZDR)
			}

		case "google":
			if err := keyring.EnsureGeminiAPIKey(); err != nil {
				fmt.Fprintf(os.Stderr, "Error ensuring Gemini API key: %v\n", err)
				os.Exit(1)
			}
			googleClient, err := googleprov.New(ctx, googleprov.Config{
				Model:          *modelID,
				CacheThreshold: *googleCacheThreshold,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error initializing Google client: %v\n", err)
				os.Exit(1)
			}
			llmProvider = googleClient
			// Ensure cache cleanup on shutdown
			defer googleClient.Close(context.Background())
			if *verbose {
				fmt.Fprintf(os.Stderr, "[provider] google model=%s cache-threshold=%d\n", *modelID, *googleCacheThreshold)
			}

		case "bedrock":
			resolvedModel := bedrock.ResolveModelID(*modelID)
			pool, err := bedrock.NewPool(ctx, bedrock.PoolConfig{
				Region:    *region,
				Profile:   *profile,
				Primary:   resolvedModel,
				Auxiliary: *auxModel,
				Planning:  *planModel,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error initializing Bedrock model pool: %v\n", err)
				os.Exit(1)
			}
			genericPool = bedrockprov.NewPool(pool)
			bedrockClient = pool.Primary()
			llmProvider = nil // TUI/Agent still use bedrockClient directly for now
			if *verbose {
				fmt.Fprintf(os.Stderr, "[pool] primary=%s auxiliary=%s planning=%s\n",
					genericPool.Info(string(bedrock.RolePrimary)).DisplayName,
					genericPool.Info(string(bedrock.RoleAuxiliary)).DisplayName,
					genericPool.Info(string(bedrock.RolePlanning)).DisplayName,
				)
			}

		default:
			fmt.Fprintf(os.Stderr, "Unknown provider: %s (supported: bedrock, google, ollama, openrouter)\n", providerName)
			os.Exit(1)
		}
	}

	// Initialize prompt manager
	promptMgr, err := prompt.NewManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing prompt manager: %v\n", err)
		os.Exit(1)
	}

	// Initialize plugin manager and discover plugins
	pluginMgr := pluginhost.NewManager(*verbose)
	defer pluginMgr.Shutdown()

	if *pluginDir != "" {
		if err := pluginMgr.DiscoverPlugins(ctx, *pluginDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering plugins: %v\n", err)
			os.Exit(1)
		}
	}

	// Build system prompt
	var promptTools []prompt.ToolDef
	for _, def := range pluginMgr.Definitions() {
		promptTools = append(promptTools, prompt.ToolDef{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  prompt.SchemaToToolParams(def.InputSchema),
		})
	}
	for _, def := range conversation.BuiltinToolDefs(morph) {
		promptTools = append(promptTools, prompt.ToolDef{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  prompt.SchemaToToolParams(def.InputSchema),
		})
	}
	modelDisplayID := *modelID
	if bedrockClient != nil {
		modelDisplayID = bedrockClient.ModelID()
	} else if llmProvider != nil {
		modelDisplayID = llmProvider.Name()
	}
	// Render the primary system prompt without swarm nodes (REPL mode is single-agent)
	systemPrompt, err := promptMgr.RenderSystem(*workDir, modelDisplayID, providerName, promptTools, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error rendering system prompt: %v\n", err)
		os.Exit(1)
	}
	// Append LLM context hints from plugins
	if hints := pluginMgr.LLMHints(); hints != "" {
		systemPrompt += "\n\n## Additional Tool Guidance\n" + hints
	}

	// One-shot mode: no TUI, just print result to stdout
	if *oneShot != "" {
		runOneShot(ctx, bedrockClient, genericPool, llmProvider, pluginMgr, store, *sessionID, systemPrompt, *workDir, *maxSteps, *verbose, *autoApprove, auditLogger, *oneShot, morph)
		return
	}

	// Interactive mode
	if *noTUI {
		// Initialize the Agent for REPL
		agent, err := conversation.NewAgent(conversation.Config{
			Client:      bedrockClient,
			Pool:        genericPool,
			Provider:    llmProvider,
			Morph:       morph,
			PromptMgr:   promptMgr,
			PluginMgr:   pluginMgr,
			WorkDir:     *workDir,
			MaxSteps:    *maxSteps,
			Verbose:     *verbose,
			AutoApprove: *autoApprove,
			AuditLogger: auditLogger,
			Store:       store,
			SessionID:   *sessionID,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing agent: %v\n", err)
			os.Exit(1)
		}
		agent.SetSystemPrompt(systemPrompt)
		runPlainREPL(ctx, agent, store, *sessionID, *workDir, *verbose, *autoApprove, auditLogger)
		return
	}

	// Initialize the Agent for TUI
	agent, err := conversation.NewAgent(conversation.Config{
		Client:      bedrockClient,
		Pool:        genericPool,
		Provider:    llmProvider,
		Morph:       morph,
		PromptMgr:   promptMgr,
		PluginMgr:   pluginMgr,
		WorkDir:     *workDir,
		MaxSteps:    *maxSteps,
		Verbose:     *verbose,
		AutoApprove: *autoApprove,
		AuditLogger: auditLogger,
		Store:       store,
		SessionID:   *sessionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing agent: %v\n", err)
		os.Exit(1)
	}
	agent.SetSystemPrompt(systemPrompt)
	engine := conversation.NewEngine(agent)

	// Full-screen TUI mode
	model := tui.New(tui.Config{
		Client:         bedrockClient,
		Pool:           genericPool,
		Provider:       llmProvider,
		PluginMgr:      pluginMgr,
		System:         systemPrompt,
		WorkDir:        *workDir,
		Verbose:        *verbose,
		EnableThinking: *thinking,
		AutoApprove:    *autoApprove,
		Store:          store,
		SessionID:      *sessionID,
		Morph:          morph,
		Agent:          agent,
		Engine:         engine,
	})

	p := tea.NewProgram(model)
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	// Print session resume info on exit
	if fm, ok := finalModel.(tui.Model); ok {
		printSessionExit(fm.SessionID())
	}
}

// runOneShot executes a single message and exits (non-TUI, for scripting).
func runOneShot(ctx context.Context, client *bedrock.Client, pool provider.Pool, llmProvider provider.Provider, pluginMgr *pluginhost.Manager, store session.Store, sessionID, system, workDir string, maxSteps int, verbose, autoApprove bool, auditLogger *audit.Logger, message string, morph *swarm.Morphology) {
	agent, err := conversation.NewAgent(conversation.Config{
		Client:      client,
		Pool:        pool,
		Provider:    llmProvider,
		Morph:       morph,
		PromptMgr:   nil, // Not needed, system prompt already rendered
		PluginMgr:   pluginMgr,
		WorkDir:     workDir,
		MaxSteps:    maxSteps,
		Verbose:     verbose,
		AutoApprove: autoApprove,
		AuditLogger: auditLogger,
		Store:       store,
		SessionID:   sessionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing agent: %v\n", err)
		os.Exit(1)
	}
	agent.SetSystemPrompt(system)
	engine := conversation.NewEngine(agent)

	// Create a new session if not resuming
	if sessionID == "" {
		modelID := ""
		region := ""
		if client != nil {
			modelID = client.ModelID()
			region = client.Region()
		} else if llmProvider != nil {
			modelID = llmProvider.ID()
		}
		id, err := agent.InitSession(modelID, region, workDir)
		if err != nil && verbose {
			fmt.Fprintf(os.Stderr, "Warning: session creation failed: %v\n", err)
		}
		if verbose && id != "" {
			fmt.Fprintf(os.Stderr, "[session] %s\n", id)
		}
	}

	response, err := engine.Agent().Turn(ctx, message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Generate title after first turn
	if agent.SessionID() != "" {
		title := agent.GenerateTitle(ctx)
		if title != "" && store != nil {
			state, loadErr := store.Load(agent.SessionID())
			if loadErr == nil {
				state.Meta.Title = title
				store.Save(agent.SessionID(), state)
			}
		}
	}

	fmt.Println(response)
	printSessionExit(agent.SessionID())
}

func runPlainREPL(ctx context.Context, agent *conversation.Agent, store session.Store, sessionID, workDir string, verbose, autoApprove bool, auditLogger *audit.Logger) {
	// In plain REPL mode, we can prompt the user for approval via stdin
	approvalFunc := func(toolName, command, reason, risk string) bool {
		fmt.Fprintf(os.Stderr, "\n⚠️  DESTRUCTIVE OPERATION DETECTED (risk: %s)\n", risk)
		fmt.Fprintf(os.Stderr, "   Tool: %s\n", toolName)
		fmt.Fprintf(os.Stderr, "   Command: %s\n", command)
		fmt.Fprintf(os.Stderr, "   Reason: %s\n", reason)
		fmt.Fprintf(os.Stderr, "\n   Allow this operation? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		return answer == "y" || answer == "yes"
	}

	// We already have an agent, just set the approval func
	agent.SetApprovalFunc(approvalFunc)

	// Create a new session if not resuming
	if sessionID == "" {
		id, _ := agent.InitSession("", "", workDir) // IDs are handled internally if needed
		if id != "" {
			fmt.Printf("Session: %s\n", id)
		}
	} else {
		fmt.Printf("Resuming session: %s\n", sessionID)
	}

	fmt.Println("codecuttlectl - Codecuttle Meta-Harness Agent (plain mode)")
	fmt.Println("Type your message and press Enter. Type 'exit' or Ctrl+C to quit.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	firstTurn := true
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}

		response, err := agent.StreamTurn(ctx, input, func(ev conversation.StreamEvent) {
			switch ev.Type {
			case "text":
				fmt.Print(ev.Text)
			case "tool_start":
				fmt.Fprintf(os.Stderr, "\n  [tool] %s\n", ev.ToolName)
			case "tool_done":
				fmt.Fprintf(os.Stderr, "  [done] %s\n", ev.ToolName)
			case "done":
				fmt.Println()
			}
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
			continue
		}
		_ = response // already printed via streaming

		// Generate title after first turn
		if firstTurn && agent.SessionID() != "" {
			firstTurn = false
			go func() {
				title := agent.GenerateTitle(context.Background())
				if title != "" && store != nil {
					state, loadErr := store.Load(agent.SessionID())
					if loadErr == nil {
						state.Meta.Title = title
						store.Save(agent.SessionID(), state)
					}
				}
			}()
		}

		fmt.Println()
	}

	printSessionExit(agent.SessionID())
}

// runListSessions prints recent sessions in a table format.
func runListSessions(store session.Store, limit int) {
	metas, err := store.List(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing sessions: %v\n", err)
		os.Exit(1)
	}

	if len(metas) == 0 {
		fmt.Println("No sessions found.")
		return
	}

	fmt.Printf("%-14s %-30s %-20s %6s %8s %7s %s\n",
		"ID", "TITLE", "MODEL", "TURNS", "TOKENS", "COST", "AGE")
	fmt.Println(strings.Repeat("-", 110))

	for _, m := range metas {
		title := m.Title
		if title == "" {
			title = "(untitled)"
		}
		if len(title) > 28 {
			title = title[:28] + ".."
		}

		model := m.Model
		if len(model) > 18 {
			model = model[:18] + ".."
		}

		age := formatAge(m.UpdatedAt)
		totalIn := m.Stats.InputTokens + m.Stats.CacheReadInputTokens + m.Stats.CacheWriteInputTokens
		tokens := totalIn + m.Stats.OutputTokens
		cost := fmt.Sprintf("$%.2f", m.Stats.EstimatedCostUSD)

		fmt.Printf("%-14s %-30s %-20s %6d %8d %7s %s\n",
			m.ID, title, model, m.Stats.Turns, tokens, cost, age)
	}
}

// runPruneSessions removes old sessions.
func runPruneSessions(store session.Store, days int) {
	maxAge := time.Duration(days) * 24 * time.Hour
	deleted, err := store.Prune(maxAge)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error pruning sessions: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Pruned %d session(s) older than %d days.\n", deleted, days)
}

// runListModels lists available models for the given provider.
func runListModels(providerName, currentModel string) {
	// Resolve provider name
	if providerName == "" {
		if strings.HasPrefix(currentModel, "ollama:") {
			providerName = "ollama"
		} else {
			providerName = "bedrock"
		}
	}

	switch providerName {
	case "google":
		runListGoogleModels()
	case "ollama":
		fmt.Println("Ollama models — use 'ollama list' to see locally available models.")
	case "openrouter":
		fmt.Println("OpenRouter models — visit https://openrouter.ai/models for a full list.")
	case "bedrock":
		fmt.Println("AWS Bedrock models (common Anthropic models):")
		fmt.Println("  us.anthropic.claude-opus-4-6-v1          (Claude Opus 4)")
		fmt.Println("  us.anthropic.claude-sonnet-4-20250514-v1:0 (Claude Sonnet 4)")
		fmt.Println("  us.anthropic.claude-3-7-sonnet-20250219-v1:0 (Claude 3.7 Sonnet)")
		fmt.Println("  us.anthropic.claude-3-5-haiku-20241022-v1:0  (Claude 3.5 Haiku)")
		fmt.Println("\n  Use AWS CLI: aws bedrock list-foundation-models --region us-west-2")
	default:
		fmt.Fprintf(os.Stderr, "Unknown provider: %s\n", providerName)
		os.Exit(1)
	}
}

func runListGoogleModels() {
	if err := keyring.EnsureGeminiAPIKey(); err != nil {
		fmt.Fprintf(os.Stderr, "Error ensuring Gemini API key: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := googleprov.NewClientForListing(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to Google AI: %v\n", err)
		os.Exit(1)
	}

	models, err := googleprov.ListModels(ctx, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing models: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Available Google AI models:")
	fmt.Println()
	for _, m := range models {
		fmt.Printf("  %s\n", m)
	}

	// Show aliases
	aliases := googleprov.ModelAliases()
	if len(aliases) > 0 {
		fmt.Println()
		fmt.Println("Aliases (shortcuts):")
		for alias, canonical := range aliases {
			fmt.Printf("  %s -> %s\n", alias, canonical)
		}
	}
}

// formatAge returns a human-readable age string.
func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

// printSessionExit prints session resume info to stderr on exit.
func printSessionExit(sessionID string) {
	if sessionID == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "\nSession: %s\n", sessionID)
	fmt.Fprintf(os.Stderr, "Resume:  codecuttlectl --session %s\n", sessionID)
}
