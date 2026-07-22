#!/bin/bash
set -euo pipefail
echo "date_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
go version
grep -m1 '^model name' /proc/cpuinfo | sed 's/^model name[[:space:]]*://'
cat > /tmp/bodycodec_gomaxprocs.go << 'EOF'
package main
import ("fmt"; "runtime")
func main() { fmt.Printf("gomaxprocs=%d\n", runtime.GOMAXPROCS(0)) }
EOF
go run /tmp/bodycodec_gomaxprocs.go
rm -f /tmp/bodycodec_gomaxprocs.go
