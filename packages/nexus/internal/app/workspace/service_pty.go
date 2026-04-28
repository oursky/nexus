package workspace

import (
	"context"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/domain/workspace"
)

func (s *Service) cloneWithGuestIP(ctx context.Context, ws *workspace.Workspace) *workspace.Workspace {
	if ws == nil {
		return nil
	}
	out := *ws
	s.attachGuestIP(ctx, &out)
	return &out
}

func (s *Service) attachGuestIP(ctx context.Context, ws *workspace.Workspace) {
	ws.GuestIP = ""
	if s.driver == nil {
		return
	}
	if ws.State != workspace.StateRunning && ws.State != workspace.StateRestored {
		return
	}
	if !workspace.UsesGuestVM(ws.Backend) {
		return
	}
	ip, ok := s.driver.GuestSSHHost(ctx, ws.ID)
	if ok {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			ws.GuestIP = ip
		}
	}
}
