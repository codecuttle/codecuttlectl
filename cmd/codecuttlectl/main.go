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

	tea "charm.land/bubbletea/v2"

	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/conversation"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
	"github.com/codecuttle/codecuttlectl/internal/tui"
)

func main() {
	var (
		modelID   = flag.String("model", "us.anthropic.claude-opus-4-6-v1", "Bedrock model ID")
		region    = flag.String("region", "", "AWS region (default: AWS_REGION env or us-east-1)")
		profile   = flag.String("profile", "", "AWS profile name")
		workDir   = flag.String("workdir", "", "Working directory (default: current directory)")
		pluginDir = flag.String("plugin-dir", "", "Directory containing Cuttlebone plugin binaries")
		verbose   = flag.Bool("verbose", false, "Enable verbose/debug output")
		maxSteps  = flag.Int("max-steps", 25, "Maximum tool-use iterations per turn")
		thinking  = flag.Bool("thinking", false, "Enable extended thinking/reasoning (model must support it)")
		oneShot   = flag.String("message", "", "Single message mode: send this message and exit (no TUI)")
		noTUI     = flag.Bool("no-tui", false, "Disable TUI, use plain REPL mode")
	)
	flag.Parse()

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
		runOneShot(ctx, client, pluginMgr, systemPrompt, *workDir, *maxSteps, *verbose, *oneShot)
		return
	}

	// Interactive mode
	if *noTUI {
		runPlainREPL(ctx, client, pluginMgr, systemPrompt, *workDir, *maxSteps, *verbose)
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
	})

	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

// runOneShot executes a single message and exits (non-TUI, for scripting).
func runOneShot(ctx context.Context, client *bedrock.Client, pluginMgr *pluginhost.Manager, system, workDir string, maxSteps int, verbose bool, message string) {
	agent, err := conversation.NewAgent(conversation.Config{
		Client:    client,
		PromptMgr: nil, // Not needed, system prompt already rendered
		PluginMgr: pluginMgr,
		WorkDir:   workDir,
		MaxSteps:  maxSteps,
		Verbose:   verbose,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing agent: %v\n", err)
		os.Exit(1)
	}
	agent.SetSystemPrompt(system)

	response, err := agent.Turn(ctx, message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(response)
}

// runPlainREPL runs the old-style plain text REPL (fallback for non-TTY environments).
func runPlainREPL(ctx context.Context, client *bedrock.Client, pluginMgr *pluginhost.Manager, system, workDir string, maxSteps int, verbose bool) {
	agent, err := conversation.NewAgent(conversation.Config{
		Client:    client,
		PromptMgr: nil,
		PluginMgr: pluginMgr,
		WorkDir:   workDir,
		MaxSteps:  maxSteps,
		Verbose:   verbose,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing agent: %v\n", err)
		os.Exit(1)
	}
	agent.SetSystemPrompt(system)

	fmt.Println("codecuttlectl - Codecuttle Meta-Harness Agent (plain mode)")
	fmt.Printf("Model: %s | Region: %s | Plugins: %d\n", client.ModelID(), client.Region(), pluginMgr.Count())
	fmt.Println("Type your message and press Enter. Type 'exit' or Ctrl+C to quit.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

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

		response, err := agent.Turn(ctx, input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}
		fmt.Println()
		fmt.Println(response)
		fmt.Println()
	}
}
