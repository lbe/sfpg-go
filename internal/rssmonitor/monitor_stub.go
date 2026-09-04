//go:build !linux

package rssmonitor

import "context"

func run(ctx context.Context, cfg Config) {}
