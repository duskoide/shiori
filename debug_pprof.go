//go:build pprof

package main

// Profiling build: go build -tags pprof, then run with
// HERDR_MYGO_PPROF=127.0.0.1:6060 and use go tool pprof on
// http://127.0.0.1:6060/debug/pprof/{profile,heap,...}.

import (
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"runtime/pprof"
	"time"
)

func init() {
	// HERDR_MYGO_CPUPROFILE=file profiles the first 3 seconds (startup).
	if path := os.Getenv("HERDR_MYGO_CPUPROFILE"); path != "" {
		if f, err := os.Create(path); err == nil && pprof.StartCPUProfile(f) == nil {
			time.AfterFunc(3*time.Second, func() { pprof.StopCPUProfile(); f.Close() })
		}
	}
	addr := os.Getenv("HERDR_MYGO_PPROF")
	if addr == "" {
		return
	}
	runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(100000)
	go http.ListenAndServe(addr, nil)
}
