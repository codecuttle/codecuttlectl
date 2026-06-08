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
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
	"github.com/codecuttle/codecuttlectl/internal/session"
	"github.com/codecuttle/codecuttlectl/internal/tui"
)

func main() {
	var (
		modelID   = flag.String("model", "us.anthropic.claude-opus-4-6-v1", "Bedrock model ID")
		region    = flag.String("region", "", "AWS region (default: AWS_REGION env or us-west-2)")
		profile   = flag.String("profile", "", "AWS profile name")
		workDir   = flag.String("workdir", "", "Working directory (default: current directory)")
		pluginDir = flag.String("plugin-dir", "", "Directory containing Cuttlebone plugin binaries")
		verbose   = flag.Bool("verbose", false, "Enable verbose/debug output")
		maxSteps  = flag.Int("max-steps", 25, "Maximum tool-use iterations per turn")
		thinking  = flag.Bool("thinking", false, "Enable extended thinking/reasoning (model must support it)")
		oneShot   = flag.String("message", "", "Single message mode: send this message and exit (no TUI)")
		noTUI     = flag.Bool("no-tui", false, "Disable TUI, use plain REPL mode")

		// Session management
		sessionID     = flag.String("session", "", "Resume an existing session by ID")
		listSessions  = flag.Bool("list-sessions", false, "Show recent sessions and exit")
		sessionLimit  = flag.Int("session-limit", 20, "Number of sessions to show in list")
		pruneSessions = flag.Int("prune-sessions", 0, "Delete sessions older than N days and exit")

		// Safety
		autoApprove   = flag.Bool("auto-approve", false, "Skip confirmation prompts for destructive operations (use in automated pipelines)")
		auditLog      = flag.Bool("audit-log", false, "Emit structured JSON audit events to stderr")
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

	// Initialize Bedrock client
	client, err := bedrock.NewClient(ctx, bedrock.Config{
		Region:  *region,
		ModelID: *modelID,
		Profile: *profile,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing Bedrock client: %v\n", err)
		os.Exit(1)
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
		})
	}
	systemPrompt, err := promptMgr.RenderSystem(*workDir, client.ModelID(), promptTools)
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
		runOneShot(ctx, client, pluginMgr, store, *sessionID, systemPrompt, *workDir, *maxSteps, *verbose, *autoApprove, auditLogger, *oneShot)
		return
	}

	// Interactive mode
	if *noTUI {
		runPlainREPL(ctx, client, pluginMgr, store, *sessionID, systemPrompt, *workDir, *maxSteps, *verbose, *autoApprove, auditLogger)
		return
	}

	// Full-screen TUI mode
	model := tui.New(tui.Config{
		Client:         client,
		PluginMgr:      pluginMgr,
		System:         systemPrompt,
		WorkDir:        *workDir,
		Verbose:        *verbose,
		EnableThinking: *thinking,
		AutoApprove:    *autoApprove,
		Store:          store,
		SessionID:      *sessionID,
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
func runOneShot(ctx context.Context, client *bedrock.Client, pluginMgr *pluginhost.Manager, store session.Store, sessionID, system, workDir string, maxSteps int, verbose, autoApprove bool, auditLogger *audit.Logger, message string) {
	agent, err := conversation.NewAgent(conversation.Config{
		Client:      client,
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

	// Create a new session if not resuming
	if sessionID == "" {
		id, err := agent.InitSession(client.ModelID(), client.Region(), workDir)
		if err != nil && verbose {
			fmt.Fprintf(os.Stderr, "Warning: session creation failed: %v\n", err)
		}
		if verbose && id != "" {
			fmt.Fprintf(os.Stderr, "[session] %s\n", id)
		}
	}

	response, err := agent.Turn(ctx, message)
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

// runPlainREPL runs the old-style plain text REPL (fallback for non-TTY environments).
func runPlainREPL(ctx context.Context, client *bedrock.Client, pluginMgr *pluginhost.Manager, store session.Store, sessionID, system, workDir string, maxSteps int, verbose, autoApprove bool, auditLogger *audit.Logger) {
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

	agent, err := conversation.NewAgent(conversation.Config{
		Client:       client,
		PromptMgr:    nil,
		PluginMgr:    pluginMgr,
		WorkDir:      workDir,
		MaxSteps:     maxSteps,
		Verbose:      verbose,
		AutoApprove:  autoApprove,
		ApprovalFunc: approvalFunc,
		AuditLogger:  auditLogger,
		Store:        store,
		SessionID:    sessionID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing agent: %v\n", err)
		os.Exit(1)
	}
	agent.SetSystemPrompt(system)

	// Create a new session if not resuming
	if sessionID == "" {
		id, _ := agent.InitSession(client.ModelID(), client.Region(), workDir)
		if id != "" {
			fmt.Printf("Session: %s\n", id)
		}
	} else {
		fmt.Printf("Resuming session: %s\n", sessionID)
	}

	fmt.Println("codecuttlectl - Codecuttle Meta-Harness Agent (plain mode)")
	fmt.Printf("Model: %s | Region: %s | Plugins: %d\n", client.ModelID(), client.Region(), pluginMgr.Count())
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

	fmt.Printf("%-14s %-30s %-20s %6s %8s %s\n",
		"ID", "TITLE", "MODEL", "TURNS", "TOKENS", "AGE")
	fmt.Println(strings.Repeat("-", 100))

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
		tokens := m.Stats.InputTokens + m.Stats.OutputTokens

		fmt.Printf("%-14s %-30s %-20s %6d %8d %s\n",
			m.ID, title, model, m.Stats.Turns, tokens, age)
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
