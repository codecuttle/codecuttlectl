package keyring

import (
	"os"
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
