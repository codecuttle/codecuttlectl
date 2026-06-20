package keyring

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	zkeyring "github.com/zalando/go-keyring"
	"golang.org/x/term"
)

// EnsureGeminiAPIKey checks if the GEMINI_API_KEY environment variable is set.
// If not, it checks the system keyring. If still not found, it prompts the
// user interactively and offers to save it to the keyring for future sessions.
func EnsureGeminiAPIKey() error {
	const envKey = "GEMINI_API_KEY"
	const service = "codecuttlectl"

	// 1. Check if already in environment (e.g. set by user shell)
	if key := os.Getenv(envKey); key != "" {
		return nil
	}

	// 2. Check system keyring
	key, err := zkeyring.Get(service, envKey)
	if err == nil && key != "" {
		// Found in keyring, inject into environment for genai client
		return os.Setenv(envKey, key)
	}

	// 3. Prompt user interactively
	fmt.Fprintf(os.Stderr, "GEMINI_API_KEY is not set.\n")
	fmt.Fprintf(os.Stderr, "You can get an API key from https://aistudio.google.com/app/apikey\n")
	fmt.Fprintf(os.Stderr, "Enter your GEMINI_API_KEY: ")

	// Read password securely
	byteKey, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n")
		return fmt.Errorf("failed to read API key: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n") // ReadPassword doesn't echo the newline

	apiKey := strings.TrimSpace(string(byteKey))
	if apiKey == "" {
		return fmt.Errorf("API key cannot be empty")
	}

	// 4. Offer to save to keyring
	fmt.Fprintf(os.Stderr, "Save API key to system keyring for future sessions? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response == "y" || response == "yes" {
		if err := zkeyring.Set(service, envKey, apiKey); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save to keyring: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "API key saved to system keyring.\n")
		}
	}

	// 5. Set in environment for the current session
	return os.Setenv(envKey, apiKey)
}
