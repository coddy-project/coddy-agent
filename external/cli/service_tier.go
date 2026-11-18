//go:build cli

package cli

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func (a *App) serviceTierOption() *acp.ConfigOption {
	for i := range a.configOpts {
		if a.configOpts[i].ID == "service_tier" {
			return &a.configOpts[i]
		}
	}
	return nil
}

func (a *App) adoptServiceTierOption(snap acp.SessionSettings) {
	opts := make([]acp.ConfigOption, 0, len(a.configOpts)+1)
	for _, opt := range a.configOpts {
		if opt.ID != "service_tier" {
			opts = append(opts, opt)
		}
	}
	if len(snap.ServiceTierChoices) > 0 {
		opts = append(opts, session.ServiceTierOption(snap.ServiceTier, snap.ServiceTierChoices))
	}
	a.configOpts = opts
}

func (a *App) openServiceTierSelector() {
	if a.busyWithLocalShell() {
		return
	}
	opt := a.serviceTierOption()
	if opt == nil {
		a.appendStatus(roleDim, "No service tiers are available for this model")
		return
	}
	a.openConfigSelector("Select service tier", opt, func(value string) {
		a.applySettings(session.SettingsChange{ServiceTier: &value})
	})
}

func (a *App) rememberServiceTier(value string) {
	home := a.config().Paths.Home
	st := loadConsoleState(home)
	st.LastServiceTier = value
	saveConsoleState(home, st)
}

func (a *App) applyInitialServiceTier(ctx context.Context, res *acp.SessionNewResult) error {
	tier := loadConsoleState(a.config().Paths.Home).LastServiceTier
	if tier == "" {
		return nil
	}
	for _, opt := range res.ConfigOptions {
		if opt.ID != "service_tier" {
			continue
		}
		for _, v := range opt.Options {
			if v.Value != tier {
				continue
			}
			result, err := a.mgr.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{SessionID: res.SessionID, ConfigID: "service_tier", Value: tier})
			if err != nil {
				return err
			}
			if result != nil {
				res.ConfigOptions = result.ConfigOptions
			}
			return nil
		}
	}
	return nil
}
