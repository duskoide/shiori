package app

import (
	"os"
	"testing"
	"time"
)

func TestDbgSvc(t *testing.T) {
	a := svcApp(t, "exit", "startup")
	st := a.mods["helper"]
	t.Logf("on=%v err=%v run=%v", st.on, st.Err, st.Manifest.Run)
	a.startServices()
	t.Logf("svc=%+v", st.svc)
	time.Sleep(500 * time.Millisecond)
	a.runPosted()
	t.Logf("svc after=%+v", a.mods["helper"].svc)
	b, _ := os.ReadFile(st.Dir + "/log")
	t.Logf("log %q", b)
}
