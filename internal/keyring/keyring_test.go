package keyring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	zkeyring "github.com/zalando/go-keyring"
)

func TestEnsureGeminiAPIKey_EnvAlreadySet(t *testing.T) {
	// Set environment variable temporarily
	t.Setenv("GEMINI_API_KEY", "test-env-key")

	err := EnsureGeminiAPIKey()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
}

func TestEnsureGeminiAPIKey_KeyringFallback(t *testing.T) {
	// Clear the environment variable
	t.Setenv("GEMINI_API_KEY", "")

	// Set up mock keyring
	zkeyring.MockInit()
	
	// Pre-populate the mock keyring
	err := zkeyring.Set("codecuttlectl", "GEMINI_API_KEY", "test-keyring-key")
	if err != nil {
		t.Fatalf("Failed to set mock keyring: %v", err)
	}

	err = EnsureGeminiAPIKey()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify the environment variable was injected
	if key := os.Getenv("GEMINI_API_KEY"); key != "test-keyring-key" {
		t.Errorf("Expected environment variable to be set to 'test-keyring-key', got '%s'", key)
	}
}

func TestEnsureGeminiAPIKey_LocalFileFallback(t *testing.T) {
	// Clear the environment variable
	t.Setenv("GEMINI_API_KEY", "")
	
	// Create a temporary XDG_CONFIG_HOME for testing
	tmpDir, err := os.MkdirTemp("", "codecuttlectl-test-config-*")
	if err != nil {
		t.Fatalf("Failed to create temp config dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Pre-populate the local credentials file
	cfgDir := filepath.Join(tmpDir, "codecuttlectl")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatalf("Failed to create codecuttlectl dir: %v", err)
	}
	
	credPath := filepath.Join(cfgDir, "credentials.json")
	credData := map[string]string{"GEMINI_API_KEY": "test-file-key"}
	data, _ := json.Marshal(credData)
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		t.Fatalf("Failed to write mock credentials: %v", err)
	}

	// Tell the mock keyring to pretend it has nothing
	zkeyring.MockInit()

	err = EnsureGeminiAPIKey()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify the environment variable was injected from the file
	if key := os.Getenv("GEMINI_API_KEY"); key != "test-file-key" {
		t.Errorf("Expected environment variable to be set to 'test-file-key', got '%s'", key)
	}
}

