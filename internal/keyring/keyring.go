package keyring

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	zkeyring "github.com/zalando/go-keyring"
	"golang.org/x/term"
)

// EnsureGeminiAPIKey checks if the GEMINI_API_KEY environment variable is set.
// If not, it checks the system keyring or local credentials file. If still not found,
// it prompts the user interactively and offers to save it for future sessions.
func EnsureGeminiAPIKey() error {
	return ensureAPIKey("GEMINI_API_KEY", "https://aistudio.google.com/app/apikey")
}

// EnsureOpenRouterAPIKey checks if the OPENROUTER_API_KEY environment variable is set.
// If not, it checks the system keyring or local credentials file. If still not found,
// it prompts the user interactively and offers to save it for future sessions.
func EnsureOpenRouterAPIKey() error {
	return ensureAPIKey("OPENROUTER_API_KEY", "https://openrouter.ai/keys")
}

func ensureAPIKey(envKey, url string) error {
	const service = "codecuttlectl"

	// 1. Check if already in environment (e.g. set by user shell)
	if key := os.Getenv(envKey); key != "" {
		return nil
	}

	// 2. Check system keyring
	key, err := zkeyring.Get(service, envKey)
	if err == nil && key != "" {
		// Found in keyring, inject into environment for client
		return os.Setenv(envKey, key)
	}

	// 2b. Check local fallback credentials file (headless servers)
	if key := getFallbackCredential(envKey); key != "" {
		return os.Setenv(envKey, key)
	}

	// 3. Prompt user interactively
	fmt.Fprintf(os.Stderr, "%s is not set.\n", envKey)
	fmt.Fprintf(os.Stderr, "You can get an API key from %s\n", url)
	fmt.Fprintf(os.Stderr, "Enter your %s: ", envKey)

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

	// 4. Offer to save to keyring / fallback store
	fmt.Fprintf(os.Stderr, "Save API key for future sessions? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response == "y" || response == "yes" {
		if err := zkeyring.Set(service, envKey, apiKey); err != nil {
			// Keyring failed (typical on headless Linux without DBus/SecretService)
			// Fallback to local credentials file with 0600 permissions
			if err := setFallbackCredential(envKey, apiKey); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save credential: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "API key saved to local credentials file (~/.config/codecuttlectl/credentials.json).\n")
			}
		} else {
			fmt.Fprintf(os.Stderr, "API key saved to system keyring.\n")
		}
	}

	// 5. Set in environment for the current session
	return os.Setenv(envKey, apiKey)
}

func configDir() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "codecuttlectl")
}

func credentialsPath() string {
	return filepath.Join(configDir(), "credentials.json")
}

func getFallbackCredential(key string) string {
	data, err := os.ReadFile(credentialsPath())
	if err != nil {
		return ""
	}

	var creds map[string]string
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}
	return creds[key]
}

func setFallbackCredential(key, value string) error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	path := credentialsPath()
	creds := make(map[string]string)

	// Read existing if present
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &creds)
	}

	creds[key] = value

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}

	// Write with strict 0600 permissions
	return os.WriteFile(path, data, 0600)
}
