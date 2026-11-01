package app

import (
	"fmt"

	"github.com/duskoide/shiori/internal/mods"
)

// service is a mod's running service.
type service struct{ st *modState }

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

func (a *App) stopService(st *modState, why string) {}

func (a *App) startServices() {}

func (a *App) runOnService(st *modState, mc mods.Command, v args) (any, error) {
	return nil, fmt.Errorf("mod %q: its service cannot run yet", st.ID)
}
