//go:build !linux

package rssmonitor

import (
	"context"
	"testing"
)

func TestRun_Noop(t *testing.T) {
	Run(context.Background(), Config{})
}
