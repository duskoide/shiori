package app

import "fmt"

// attachService ties a session to the mod whose service it is.
func (a *App) attachService(s *rpcSession, mod, token string) error {
	return fmt.Errorf("no mod %q is running", mod)
}

// detachService forgets a session that was a mod's service.
func (a *App) detachService(*rpcSession) {}

// wantEvents are the events services that are not connected wait for.
func (a *App) wantEvents() []string { return nil }

// eventForServices hands an event to the services that are not connected.
func (a *App) eventForServices(string, []byte) {}
