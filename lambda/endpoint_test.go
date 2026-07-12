package lambda

import "testing"

// TestEndpointEnvIncludesEndpoint proves the configured stack Endpoint is
// injected as AWS_ENDPOINT_URL so function code using an AWS SDK reaches sibling
// services (previously it was never wired, so handler SDK calls failed).
func TestEndpointEnvIncludesEndpoint(t *testing.T) {
	s, err := New(Options{DataDir: t.TempDir(), Endpoint: "http://127.0.0.1:14599"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env := s.endpointEnv()
	if got := env["AWS_ENDPOINT_URL"]; got != "http://127.0.0.1:14599" {
		t.Fatalf("AWS_ENDPOINT_URL = %q, want the configured endpoint", got)
	}
}

// TestEndpointEnvOmitsEndpointWhenUnset: with no endpoint configured, the SDK
// generic variable is absent (embedded, listener-less mode).
func TestEndpointEnvOmitsEndpointWhenUnset(t *testing.T) {
	s, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, ok := s.endpointEnv()["AWS_ENDPOINT_URL"]; ok {
		t.Fatal("AWS_ENDPOINT_URL should be absent when no endpoint is configured")
	}
}
