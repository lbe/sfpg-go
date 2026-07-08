package config

import (
	"context"
	"fmt"
	"testing"
)

func TestEnsureDefaults_Delegates(t *testing.T) {
	service := &fakeService{}
	EnsureDefaults(context.Background(), "/tmp", service, nil)
	if service.ensureRoot != "/tmp" {
		t.Fatalf("expected EnsureDefaults to be called with rootDir /tmp, got %q", service.ensureRoot)
	}
}

func TestEnsureDefaults_NoService(t *testing.T) {
	EnsureDefaults(context.Background(), "/tmp", nil, nil)
}

func TestEnsureDefaults_PanicsOnError(t *testing.T) {
	service := &fakeService{ensureErr: fmt.Errorf("boom")}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when EnsureDefaults returns error")
		}
	}()
	EnsureDefaults(context.Background(), "/tmp", service, nil)
}
